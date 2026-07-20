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

func sqliteAttributionReleasedCommitFixture(t *testing.T, value string) (*sqliteAttributionConnection, *sqliteAttributionTx, SQLiteHoldSession, string, SQLiteTransaction, int) {
	t.Helper()
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
	transaction, err := connection.BeginTx(context.Background(), driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	statement, err := connection.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := statement.Exec([]driver.Value{value}); err != nil {
		t.Fatal(err)
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	session, err := sqliteAttributionTracker.Load().ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	wrapped := transaction.(*sqliteAttributionTx)
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("selected transaction is not active")
	}
	return connection, wrapped, session, databasePath, identity, descriptor
}

func sqliteAttributionReleasedTerminalSnapshot(t *testing.T, session SQLiteHoldSession) SQLiteHoldSnapshot {
	t.Helper()
	terminal, err := sqliteAttributionTracker.Load().WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
	if err != nil {
		t.Fatal(err)
	}
	return terminal
}

func TestSQLiteAttributionReleasedCommitOwnsTerminalAgainstConcurrentRollback(t *testing.T) {
	// Given
	connection, transaction, session, databasePath, identity, descriptor := sqliteAttributionReleasedCommitFixture(t, "released-rollback-owner")
	t.Cleanup(func() {
		if err := connection.Close(); err != nil && !errors.Is(err, driver.ErrBadConn) {
			t.Error(err)
		}
	})
	releasedBoundary := make(chan struct{})
	allowCommitClaim := make(chan struct{})
	loserWaiting := make(chan struct{})
	transaction.state.releasedCommitBoundary = func() {
		close(releasedBoundary)
		<-allowCommitClaim
	}
	transaction.state.terminalLoserWaitBoundary = func() { close(loserWaiting) }
	finalizing := make(chan error, 1)
	commitResult := make(chan error, 1)
	go func() {
		_, err := sqliteAttributionTracker.Load().WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
		finalizing <- err
	}()
	go func() { commitResult <- transaction.Commit() }()
	if err := <-finalizing; err != nil {
		t.Fatal(err)
	}

	// When
	if err := sqliteAttributionTracker.Load().ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	<-releasedBoundary
	rollbackResult := make(chan error, 1)
	go func() { rollbackResult <- transaction.Rollback() }()
	loserReturned := false
	var rollbackErr error
	select {
	case <-loserWaiting:
	case rollbackErr = <-rollbackResult:
		loserReturned = true
	}
	close(allowCommitClaim)
	commitErr := <-commitResult
	if !loserReturned {
		rollbackErr = <-rollbackResult
	}
	terminal := sqliteAttributionReleasedTerminalSnapshot(t, session)
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
	count := sqliteAttributionPersistedCount(t, databasePath)

	// Then
	if loserReturned {
		t.Errorf("released Commit lost terminal ownership: Rollback returned %v before waiting", rollbackErr)
	}
	if commitErr != nil {
		t.Errorf("released Commit error = %v, want nil", commitErr)
	}
	if !errors.Is(rollbackErr, driver.ErrBadConn) {
		t.Errorf("Rollback after successful Release error = %v, want driver.ErrBadConn", rollbackErr)
	}
	if !terminal.Selected || !terminal.Finalizing || !terminal.Released {
		t.Errorf("released terminal snapshot = %+v", terminal)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Errorf("journal descriptor after released Commit = %v, want EBADF", descriptorErr)
	}
	if count != 1 {
		t.Errorf("released Commit persisted %d rows, want 1", count)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Error("released Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Error("released Commit left the tracker transaction active")
	}
}

func TestSQLiteAttributionReleasedCommitOwnsTerminalAgainstConcurrentConnectionClose(t *testing.T) {
	// Given
	connection, transaction, session, databasePath, identity, descriptor := sqliteAttributionReleasedCommitFixture(t, "released-close-owner")
	connectionClosed := false
	t.Cleanup(func() {
		if !connectionClosed {
			if err := connection.Close(); err != nil && !errors.Is(err, driver.ErrBadConn) {
				t.Error(err)
			}
		}
	})
	releasedBoundary := make(chan struct{})
	allowCommitClaim := make(chan struct{})
	loserWaiting := make(chan struct{})
	transaction.state.releasedCommitBoundary = func() {
		close(releasedBoundary)
		<-allowCommitClaim
	}
	transaction.state.terminalLoserWaitBoundary = func() { close(loserWaiting) }
	finalizing := make(chan error, 1)
	commitResult := make(chan error, 1)
	go func() {
		_, err := sqliteAttributionTracker.Load().WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
		finalizing <- err
	}()
	go func() { commitResult <- transaction.Commit() }()
	if err := <-finalizing; err != nil {
		t.Fatal(err)
	}

	// When
	if err := sqliteAttributionTracker.Load().ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	<-releasedBoundary
	closeResult := make(chan error, 1)
	go func() { closeResult <- connection.Close() }()
	loserReturned := false
	var closeErr error
	select {
	case <-loserWaiting:
	case closeErr = <-closeResult:
		loserReturned = true
	}
	close(allowCommitClaim)
	commitErr := <-commitResult
	if !loserReturned {
		closeErr = <-closeResult
	}
	connectionClosed = true
	terminal := sqliteAttributionReleasedTerminalSnapshot(t, session)
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM settings").Scan(&count); err != nil {
		t.Fatal(err)
	}

	// Then
	if loserReturned {
		t.Errorf("released Commit lost terminal ownership: connection Close returned %v before waiting", closeErr)
	}
	if commitErr != nil {
		t.Errorf("released Commit error = %v, want nil", commitErr)
	}
	if closeErr != nil && !errors.Is(closeErr, driver.ErrBadConn) {
		t.Errorf("connection Close after successful Release error = %v, want nil or driver.ErrBadConn", closeErr)
	}
	if !terminal.Selected || !terminal.Finalizing || !terminal.Released {
		t.Errorf("released terminal snapshot = %+v", terminal)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Errorf("journal descriptor after released Commit = %v, want EBADF", descriptorErr)
	}
	if count != 1 {
		t.Errorf("released Commit persisted %d rows, want 1", count)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Error("released Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Error("released Commit left the tracker transaction active")
	}
}
