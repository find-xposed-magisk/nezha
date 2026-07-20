//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

type sqliteAttributionLegacyQueryStmt struct {
	closeCount int
}

func (statement *sqliteAttributionLegacyQueryStmt) Close() error { statement.closeCount++; return nil }
func (sqliteAttributionLegacyQueryStmt) NumInput() int           { return -1 }
func (sqliteAttributionLegacyQueryStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, driver.ErrSkip
}
func (sqliteAttributionLegacyQueryStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, driver.ErrSkip
}

type sqliteAttributionCountingStmt struct {
	driver.Stmt
	closeCount int
}

func (statement *sqliteAttributionCountingStmt) Close() error {
	statement.closeCount++
	return statement.Stmt.Close()
}

type sqliteAttributionQueryProbeStmt struct {
	closeCount int
	queryCount int
}

func (statement *sqliteAttributionQueryProbeStmt) Close() error { statement.closeCount++; return nil }
func (sqliteAttributionQueryProbeStmt) NumInput() int           { return -1 }
func (sqliteAttributionQueryProbeStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, driver.ErrSkip
}
func (statement *sqliteAttributionQueryProbeStmt) Query([]driver.Value) (driver.Rows, error) {
	statement.queryCount++
	return nil, driver.ErrSkip
}

func TestSQLiteAttributionDirectQueryClosesOwnedStatementWhenPreStepRejects(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	rawConnection, err := sqliteAttributionDriver{}.Open(sqliteAttributionTestDatabasePath(t))
	if err != nil {
		t.Fatal(err)
	}
	connection := rawConnection.(*sqliteAttributionConnection)
	t.Cleanup(func() {
		if closeErr := connection.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := connection.connection.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	prepared, err := connection.Prepare("INSERT INTO settings (value) VALUES (?) RETURNING id")
	if err != nil {
		t.Fatal(err)
	}
	statement := prepared.(*sqliteAttributionStatement)
	owner := &sqliteAttributionCountingStmt{Stmt: prepared}
	enableSQLiteAttribution()

	// When
	rows, queryErr := statement.queryOwned(func() (driver.Rows, error) {
		return statement.statement.Query([]driver.Value{"rejected"})
	}, owner)

	// Then
	if rows != nil {
		t.Fatal("rejected direct Query returned rows")
	}
	if !errors.Is(queryErr, ErrSQLiteAttributionUnboundWrite) {
		t.Fatalf("rejected direct Query error = %v", queryErr)
	}
	if owner.closeCount != 1 {
		t.Fatalf("owned statement Close count = %d, want 1", owner.closeCount)
	}
}

func TestSQLiteAttributionDirectQueryRejectsUnboundWriteThroughPublicConnection(t *testing.T) {
	resetSQLiteAttributionForTest()
	probe := &sqliteAttributionQueryProbeStmt{}
	var connection *sqliteAttributionConnection
	connection = &sqliteAttributionConnection{
		prepareStatement: func(context.Context, string) (driver.Stmt, error) {
			return &sqliteAttributionStatement{
				connection: connection,
				statement:  probe,
				classification: sqliteAttributionClassification{
					hasRowDML: true,
					operation: SQLiteOperationInsert,
					table:     "settings",
				},
			}, nil
		},
	}
	enableSQLiteAttribution()

	rows, queryErr := connection.Query("INSERT INTO settings (value) VALUES (?) RETURNING id", []driver.Value{"public-rejected"})

	if rows != nil {
		t.Fatal("public direct Query returned rows after pre-step rejection")
	}
	if !errors.Is(queryErr, ErrSQLiteAttributionUnboundWrite) {
		t.Fatalf("public direct Query error = %v", queryErr)
	}
	if probe.closeCount != 1 {
		t.Fatalf("public direct Query statement Close count = %d, want 1", probe.closeCount)
	}
	if probe.queryCount != 0 {
		t.Fatalf("public direct Query invoked underlying Query %d times, want 0", probe.queryCount)
	}
}

func TestSQLiteAttributionQueryContextClosesUnsupportedStatementBeforeErrSkip(t *testing.T) {
	// Given
	statement := &sqliteAttributionLegacyQueryStmt{}
	connection := &sqliteAttributionConnection{}
	wrapped := &sqliteAttributionStatement{connection: connection, statement: statement}

	// When
	rows, queryErr := connection.queryContextStatement(context.Background(), wrapped, nil, statement)

	// Then
	if rows != nil {
		t.Fatal("unsupported QueryContext returned rows")
	}
	if !errors.Is(queryErr, driver.ErrSkip) {
		t.Fatalf("unsupported QueryContext error = %v", queryErr)
	}
	if statement.closeCount != 1 {
		t.Fatalf("unsupported QueryContext Close count = %d, want 1", statement.closeCount)
	}
}
