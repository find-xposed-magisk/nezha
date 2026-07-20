//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mattn/go-sqlite3"
)

const sqliteAttributionDriverName = "nezha_sqlite_attribution"

var (
	ErrSQLiteAttributionUnsupportedDSN   = errors.New("sqlite attribution requires a filesystem database")
	ErrSQLiteAttributionUnboundWrite     = errors.New("sqlite attribution observed a write without an explicit transaction")
	ErrSQLiteAttributionJournalIdentity  = errors.New("sqlite attribution could not identify the rollback journal")
	ErrSQLiteAttributionUnsupportedWrite = errors.New("sqlite attribution cannot classify this write")

	sqliteAttributionDriverRegistration sync.Once
	sqliteAttributionEnabled            atomic.Bool
	sqliteAttributionConnectionID       atomic.Uint64
	sqliteAttributionTransactionID      atomic.Uint64
	sqliteAttributionTracker            atomic.Pointer[SQLiteHoldTracker]
	sqliteAttributionHoldControl        *sqliteHoldControl
)

type SQLiteAttributionError struct{ Cause error }

func (err *SQLiteAttributionError) Error() string { return err.Cause.Error() }
func (err *SQLiteAttributionError) Unwrap() error { return err.Cause }

func init() { resetSQLiteAttributionControl() }

func registerSQLiteAttributionDriver() {
	sqliteAttributionDriverRegistration.Do(func() { sql.Register(sqliteAttributionDriverName, sqliteAttributionDriver{}) })
}

func enableSQLiteAttribution() { sqliteAttributionEnabled.Store(true) }

func resetSQLiteAttributionForTest() {
	sqliteAttributionEnabled.Store(false)
	resetSQLiteAttributionControl()
}

func resetSQLiteAttributionControl() {
	tracker := newSQLiteAttributionHoldTracker(&sqliteAttributionEnabled)
	sqliteAttributionTracker.Store(tracker)
	sqliteAttributionHoldControl = newProductionSQLiteHoldControl(tracker)
}

type sqliteAttributionDriver struct{}

func (sqliteAttributionDriver) Open(dataSourceName string) (driver.Conn, error) {
	rawConnection, err := (&sqlite3.SQLiteDriver{}).Open(dataSourceName)
	if err != nil {
		return nil, err
	}
	connection, ok := rawConnection.(*sqlite3.SQLiteConn)
	if !ok {
		return nil, errors.New("sqlite attribution received an unsupported sqlite connection")
	}
	databasePath := connection.GetFilename("")
	if databasePath == "" {
		if closeErr := connection.Close(); closeErr != nil {
			return nil, fmt.Errorf("close unsupported sqlite database: %w", closeErr)
		}
		return nil, &SQLiteAttributionError{Cause: ErrSQLiteAttributionUnsupportedDSN}
	}
	wrapped := &sqliteAttributionConnection{connection: connection, identity: SQLiteConnectionIdentity(sqliteAttributionConnectionID.Add(1)), journal: databasePath + "-journal", closeRawConnection: connection.Close}
	wrapped.prepareStatement = wrapped.prepareSQLiteStatement
	connection.RegisterAuthorizer(wrapped.authorize)
	connection.RegisterUpdateHook(wrapped.recordUpdate)
	return wrapped, nil
}

type sqliteAttributionConnection struct {
	connection *sqlite3.SQLiteConn
	identity   SQLiteConnectionIdentity
	journal    string

	// lifecycleMu owns the active transaction and its poison, journal descriptor, and completion state; never hold it across raw SQLite, tracker, wait, or close operations.
	lifecycleMu sync.Mutex
	transaction *sqliteAttributionTransaction
	capturing   bool
	preparing   sqliteAttributionClassification
	execution   *sqliteAttributionExecution

	prepareStatement   sqliteAttributionStatementPreparer
	closeRawConnection func() error
}

type sqliteAttributionStatementPreparer func(context.Context, string) (driver.Stmt, error)

type sqliteAttributionTransaction struct {
	transaction               SQLiteTransaction
	raw                       driver.Tx
	tracker                   *SQLiteHoldTracker
	context                   context.Context
	journalFD                 int
	poison                    error
	terminalPhase             sqliteAttributionTerminalPhase
	done                      chan struct{}
	releasedCommitBoundary    func()
	terminalLoserWaitBoundary func()
}

type sqliteAttributionTerminalPhase uint8

const (
	sqliteAttributionTerminalOpen sqliteAttributionTerminalPhase = iota
	sqliteAttributionTerminalCommitWaiting
	sqliteAttributionTerminalCommitOwned
	sqliteAttributionTerminalRollbackOwned
)

type sqliteAttributionExecution struct {
	classification sqliteAttributionClassification
	origin         SQLiteExecutionOrigin
	hook           sqliteAttributionHook
}

type sqliteAttributionHook struct {
	seen     bool
	mismatch bool
}

var (
	_ driver.Driver             = sqliteAttributionDriver{}
	_ driver.Conn               = (*sqliteAttributionConnection)(nil)
	_ driver.Pinger             = (*sqliteAttributionConnection)(nil)
	_ driver.ConnPrepareContext = (*sqliteAttributionConnection)(nil)
	_ driver.ConnBeginTx        = (*sqliteAttributionConnection)(nil)
	_ driver.Execer             = (*sqliteAttributionConnection)(nil)
	_ driver.ExecerContext      = (*sqliteAttributionConnection)(nil)
	_ driver.Queryer            = (*sqliteAttributionConnection)(nil)
	_ driver.QueryerContext     = (*sqliteAttributionConnection)(nil)
)

func (connection *sqliteAttributionConnection) authorize(operation int, table, _, schema string) int {
	if !connection.capturing {
		return sqlite3.SQLITE_OK
	}
	classification := &connection.preparing
	if operation != sqlite3.SQLITE_INSERT && operation != sqlite3.SQLITE_UPDATE && operation != sqlite3.SQLITE_DELETE {
		return sqlite3.SQLITE_OK
	}
	rowOperation, ok := sqliteAttributionOperation(operation)
	if !ok || schema != "main" || strings.HasPrefix(table, "sqlite_") {
		classification.ambiguous = true
		return sqlite3.SQLITE_OK
	}
	// SQLite authorizes UPDATE once per written column; only a different row-DML target is ambiguous.
	if classification.hasRowDML {
		if classification.operation != rowOperation || classification.table != table {
			classification.ambiguous = true
		}
		return sqlite3.SQLITE_OK
	}
	classification.hasRowDML = true
	classification.operation = rowOperation
	classification.table = table
	return sqlite3.SQLITE_OK
}

func (connection *sqliteAttributionConnection) Prepare(query string) (driver.Stmt, error) {
	return connection.prepareStatement(context.Background(), query)
}

func (connection *sqliteAttributionConnection) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return connection.prepareStatement(ctx, query)
}

func (connection *sqliteAttributionConnection) prepareSQLiteStatement(ctx context.Context, query string) (driver.Stmt, error) {
	connection.preparing = sqliteAttributionClassification{}
	connection.capturing = true
	statement, err := connection.connection.PrepareContext(ctx, query)
	connection.capturing = false
	classification := connection.preparing
	connection.preparing = sqliteAttributionClassification{}
	if err != nil {
		return nil, err
	}
	rawStatement, ok := statement.(*sqlite3.SQLiteStmt)
	if !ok {
		return nil, errors.New("sqlite attribution received an unsupported sqlite statement")
	}
	classification.readonly = rawStatement.Readonly()
	return &sqliteAttributionStatement{connection: connection, statement: statement, classification: classification}, nil
}

func (connection *sqliteAttributionConnection) Ping(ctx context.Context) error {
	return connection.connection.Ping(ctx)
}
func (connection *sqliteAttributionConnection) Begin() (driver.Tx, error) {
	return connection.begin(context.Background(), driver.TxOptions{})
}
func (connection *sqliteAttributionConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return connection.begin(ctx, options)
}

func (connection *sqliteAttributionConnection) begin(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	transaction, err := connection.connection.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	identity := SQLiteTransaction{Connection: connection.identity, Identity: SQLiteTransactionIdentity(sqliteAttributionTransactionID.Add(1))}
	tracker := sqliteAttributionTracker.Load()
	if err := tracker.BeginSQLiteTransaction(identity); err != nil {
		// Tracker rejection happens after BEGIN; leave no raw transaction to wedge this connection.
		return nil, errors.Join(err, transaction.Rollback())
	}
	state := &sqliteAttributionTransaction{transaction: identity, raw: transaction, tracker: tracker, context: ctx, journalFD: -1, done: make(chan struct{})}
	connection.lifecycleMu.Lock()
	connection.transaction = state
	connection.lifecycleMu.Unlock()
	return &sqliteAttributionTx{connection: connection, state: state}, nil
}

func (connection *sqliteAttributionConnection) recordUpdate(operation int, database, table string, _ int64) {
	execution := connection.execution
	if execution == nil {
		return
	}
	rowOperation, ok := sqliteAttributionOperation(operation)
	if !ok || database != "main" || rowOperation != execution.classification.operation || table != execution.classification.table {
		execution.hook.mismatch = true
		return
	}
	execution.hook.seen = true
}

func sqliteAttributionOperation(operation int) (SQLiteOperation, bool) {
	switch operation {
	case sqlite3.SQLITE_INSERT:
		return SQLiteOperationInsert, true
	case sqlite3.SQLITE_UPDATE:
		return SQLiteOperationUpdate, true
	case sqlite3.SQLITE_DELETE:
		return SQLiteOperationDelete, true
	default:
		return "", false
	}
}
