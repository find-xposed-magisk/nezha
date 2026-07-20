//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSQLiteAttributionConnectionCloseFinalizesActiveWriteTransaction(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	databasePath := sqliteAttributionTestDatabasePath(t)
	rawConnection, err := sqliteAttributionDriver{}.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	connection := rawConnection.(*sqliteAttributionConnection)
	if _, err := connection.connection.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	transaction, err := connection.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := connection.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := statement.Exec([]driver.Value{"close-active"}); err != nil {
		t.Fatal(err)
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("active transaction is missing before Close")
	}

	// When
	firstCloseErr := connection.Close()
	secondCloseErr := connection.Close()
	commitErr := transaction.Commit()
	rollbackErr := transaction.Rollback()
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
	standard, openErr := sql.Open("sqlite3", databasePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer standard.Close()
	var count int
	countErr := standard.QueryRow("SELECT COUNT(*) FROM settings").Scan(&count)
	tracker := sqliteAttributionTracker.Load()
	tracker.mu.Lock()
	_, active = tracker.transactions[identity]
	tracker.mu.Unlock()

	// Then
	if firstCloseErr != nil || secondCloseErr != nil {
		t.Fatalf("Close errors = %v / %v", firstCloseErr, secondCloseErr)
	}
	if !errors.Is(commitErr, driver.ErrBadConn) {
		t.Fatalf("Commit after Close error = %v, want driver.ErrBadConn", commitErr)
	}
	if !errors.Is(rollbackErr, driver.ErrBadConn) {
		t.Fatalf("Rollback after Close error = %v, want driver.ErrBadConn", rollbackErr)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after Close = %v, want EBADF", descriptorErr)
	}
	if countErr != nil || count != 0 {
		t.Fatalf("closed active transaction persisted count=%d err=%v", count, countErr)
	}
	if active {
		t.Fatal("Close left the tracker transaction active")
	}
}

func TestSQLiteAttributionConnectionCloseWakesSelectedCommit(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	databasePath := sqliteAttributionTestDatabasePath(t)
	rawConnection, err := sqliteAttributionDriver{}.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	connection := rawConnection.(*sqliteAttributionConnection)
	if _, err := connection.connection.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	transaction, err := connection.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := connection.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := statement.Exec([]driver.Value{"close-wakes-commit"}); err != nil {
		t.Fatal(err)
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	_, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("active transaction is missing before selected Commit")
	}
	tracker := sqliteAttributionTracker.Load()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	finalizing := make(chan error, 1)
	commit := make(chan error, 1)
	go func() {
		_, waitErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
		finalizing <- waitErr
	}()

	// When
	go func() { commit <- transaction.Commit() }()
	if finalizingErr := <-finalizing; finalizingErr != nil {
		t.Fatalf("Commit finalization wait error = %v", finalizingErr)
	}
	closeErr := connection.Close()
	commitErr := <-commit
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
	standard, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer standard.Close()
	var count int
	if err := standard.QueryRow("SELECT COUNT(*) FROM settings").Scan(&count); err != nil {
		t.Fatal(err)
	}

	// Then
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	var holdErr *SQLiteHoldError
	if !errors.As(commitErr, &holdErr) || !errors.Is(commitErr, ErrSQLiteHoldAborted) {
		t.Fatalf("Commit error after Close = %v", commitErr)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after Close = %v, want EBADF", descriptorErr)
	}
	if count != 0 {
		t.Fatalf("Close while Commit waited persisted %d rows", count)
	}
}

type sqliteAttributionBadConnRows struct{}

func (sqliteAttributionBadConnRows) Columns() []string         { return []string{"value"} }
func (sqliteAttributionBadConnRows) Close() error              { return nil }
func (sqliteAttributionBadConnRows) Next([]driver.Value) error { return driver.ErrBadConn }

func TestSQLiteAttributionReadonlyRowsReturnBadConnWithoutPanic(t *testing.T) {
	// Given
	rows := &sqliteAttributionRows{rows: sqliteAttributionBadConnRows{}}

	// When
	err := rows.Next(make([]driver.Value, 1))

	// Then
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("readonly rows Next error = %v, want driver.ErrBadConn", err)
	}
}
