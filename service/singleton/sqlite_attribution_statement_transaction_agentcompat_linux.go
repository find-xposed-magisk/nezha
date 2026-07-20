//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
)

type sqliteAttributionClassification struct {
	readonly  bool
	hasRowDML bool
	ambiguous bool
	operation SQLiteOperation
	table     string
}

func (classification sqliteAttributionClassification) requiresAttribution() bool {
	return !classification.readonly && classification.hasRowDML
}

func (classification sqliteAttributionClassification) valid() bool {
	return !classification.ambiguous && classification.hasRowDML && classification.operation != "" && classification.table != ""
}

type sqliteAttributionStatement struct {
	connection     *sqliteAttributionConnection
	statement      driver.Stmt
	classification sqliteAttributionClassification
}

var (
	_ driver.Stmt             = (*sqliteAttributionStatement)(nil)
	_ driver.StmtExecContext  = (*sqliteAttributionStatement)(nil)
	_ driver.StmtQueryContext = (*sqliteAttributionStatement)(nil)
)

func (statement *sqliteAttributionStatement) Close() error  { return statement.statement.Close() }
func (statement *sqliteAttributionStatement) NumInput() int { return statement.statement.NumInput() }

func (statement *sqliteAttributionStatement) Exec(values []driver.Value) (driver.Result, error) {
	return statement.execute(func() (driver.Result, error) { return statement.statement.Exec(values) })
}

func (statement *sqliteAttributionStatement) ExecContext(ctx context.Context, values []driver.NamedValue) (driver.Result, error) {
	contextStatement, ok := statement.statement.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return statement.execute(func() (driver.Result, error) { return contextStatement.ExecContext(ctx, values) })
}

func (statement *sqliteAttributionStatement) Query(values []driver.Value) (driver.Rows, error) {
	return statement.queryOwned(func() (driver.Rows, error) { return statement.statement.Query(values) }, nil)
}

func (statement *sqliteAttributionStatement) QueryContext(ctx context.Context, values []driver.NamedValue) (driver.Rows, error) {
	contextStatement, ok := statement.statement.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return statement.queryOwned(func() (driver.Rows, error) { return contextStatement.QueryContext(ctx, values) }, nil)
}

func (statement *sqliteAttributionStatement) execute(run func() (driver.Result, error)) (driver.Result, error) {
	if err := statement.connection.beforeWrite(statement.classification); err != nil {
		return nil, err
	}
	result, err := run()
	if err != nil {
		statement.connection.discardExecution()
		return nil, err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows > 0 {
		if err := statement.connection.publishExecution(); err != nil {
			return nil, err
		}
	}
	statement.connection.discardExecution()
	return result, nil
}

func (statement *sqliteAttributionStatement) queryOwned(run func() (driver.Rows, error), owner driver.Stmt) (driver.Rows, error) {
	if err := statement.connection.beforeWrite(statement.classification); err != nil {
		if owner != nil {
			return nil, errors.Join(err, owner.Close())
		}
		return nil, err
	}
	rows, err := run()
	if err != nil {
		statement.connection.discardExecution()
		if owner != nil {
			return nil, errors.Join(err, owner.Close())
		}
		return nil, err
	}
	// Disabled attribution must preserve the stock driver rows lifecycle used during Dashboard bootstrap.
	if !sqliteAttributionEnabled.Load() || !statement.classification.requiresAttribution() {
		if owner == nil {
			return rows, nil
		}
		return &sqliteAttributionRows{rows: rows, owner: owner}, nil
	}
	return &sqliteAttributionRows{rows: rows, owner: owner, connection: statement.connection}, nil
}

type sqliteAttributionRows struct {
	rows       driver.Rows
	owner      driver.Stmt
	connection *sqliteAttributionConnection
	finished   bool
	closed     bool
}

func (rows *sqliteAttributionRows) Columns() []string { return rows.rows.Columns() }

func (rows *sqliteAttributionRows) ColumnTypeDatabaseTypeName(index int) string {
	typed, ok := rows.rows.(driver.RowsColumnTypeDatabaseTypeName)
	if !ok {
		return ""
	}
	return typed.ColumnTypeDatabaseTypeName(index)
}

func (rows *sqliteAttributionRows) ColumnTypeNullable(index int) (bool, bool) {
	typed, ok := rows.rows.(driver.RowsColumnTypeNullable)
	if !ok {
		return false, false
	}
	return typed.ColumnTypeNullable(index)
}

func (rows *sqliteAttributionRows) ColumnTypeScanType(index int) reflect.Type {
	typed, ok := rows.rows.(driver.RowsColumnTypeScanType)
	if !ok {
		return nil
	}
	return typed.ColumnTypeScanType(index)
}

func (rows *sqliteAttributionRows) Close() error {
	if rows.closed {
		return nil
	}
	rows.closed = true
	closeErr := rows.rows.Close()
	if !rows.finished && rows.connection != nil {
		rows.connection.poison(&SQLiteAttributionError{Cause: ErrSQLiteAttributionUnsupportedWrite})
		rows.connection.discardExecution()
	}
	if rows.owner != nil {
		return errors.Join(closeErr, rows.owner.Close())
	}
	return closeErr
}

func (rows *sqliteAttributionRows) Next(destination []driver.Value) error {
	err := rows.rows.Next(destination)
	if errors.Is(err, driver.ErrBadConn) {
		// Read-only direct Query rows have no attribution connection; ErrBadConn must still propagate.
		if rows.connection != nil {
			rows.connection.discardExecution()
		}
		return err
	}
	if err == nil {
		return nil
	}
	rows.finished = true
	if err != io.EOF {
		if rows.connection != nil {
			rows.connection.discardExecution()
		}
		return err
	}
	if rows.connection != nil {
		if publishErr := rows.connection.publishExecution(); publishErr != nil {
			return publishErr
		}
	}
	return io.EOF
}
