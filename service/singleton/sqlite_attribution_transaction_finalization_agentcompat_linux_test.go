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

func sqliteAttributionHeldTransaction(t *testing.T, value string, transactionContext context.Context) (*sqliteAttributionConnection, driver.Tx, SQLiteHoldSession, string) {
	t.Helper()
	resetSQLiteAttributionForTest()
	databasePath := sqliteAttributionTestDatabasePath(t)
	rawConnection, err := sqliteAttributionDriver{}.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	connection := rawConnection.(*sqliteAttributionConnection)
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := connection.connection.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	transaction, err := connection.BeginTx(transactionContext, driver.TxOptions{})
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
	return connection, transaction, session, databasePath
}

func sqliteAttributionStartHeldCommit(t *testing.T, transaction driver.Tx, session SQLiteHoldSession) <-chan error {
	t.Helper()
	finalizing := make(chan error, 1)
	commit := make(chan error, 1)
	go func() {
		_, err := sqliteAttributionTracker.Load().WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
		finalizing <- err
	}()
	go func() { commit <- transaction.Commit() }()
	select {
	case err := <-finalizing:
		if err != nil {
			t.Fatalf("Commit finalization wait error = %v", err)
		}
	case err := <-commit:
		t.Fatalf("Commit completed before selected hold release: %v", err)
	}
	return commit
}

func sqliteAttributionPersistedCount(t *testing.T, databasePath string) int {
	t.Helper()
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM settings").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func sqliteAttributionTransactionState(t *testing.T, connection *sqliteAttributionConnection) (SQLiteTransaction, int, bool) {
	t.Helper()
	connection.lifecycleMu.Lock()
	state := connection.transaction
	if state == nil {
		connection.lifecycleMu.Unlock()
		return SQLiteTransaction{}, -1, false
	}
	identity, descriptor := state.transaction, state.journalFD
	connection.lifecycleMu.Unlock()
	return identity, descriptor, true
}

func sqliteAttributionTrackerTransactionActive(tracker *SQLiteHoldTracker, identity SQLiteTransaction) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	_, active := tracker.transactions[identity]
	return active
}

func TestSQLiteAttributionCommitWaitsForSelectedHoldRelease(t *testing.T) {
	// Given
	connection, transaction, session, databasePath := sqliteAttributionHeldTransaction(t, "held-commit", context.Background())
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("held transaction is not active")
	}

	// When
	commit := sqliteAttributionStartHeldCommit(t, transaction, session)
	select {
	case err := <-commit:
		t.Fatalf("Commit completed while selected hold remains unreleased: %v", err)
	default:
	}
	if err := sqliteAttributionTracker.Load().ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	commitErr := <-commit

	// Then
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	if _, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0); !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after released Commit = %v, want EBADF", descriptorErr)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Fatal("released Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Fatal("released Commit left the tracker transaction active")
	}
	if count := sqliteAttributionPersistedCount(t, databasePath); count != 1 {
		t.Fatalf("released held Commit persisted %d rows", count)
	}
}

func TestSQLiteAttributionCommitRollsBackWhenSelectedHoldAborts(t *testing.T) {
	// Given
	connection, transaction, session, databasePath := sqliteAttributionHeldTransaction(t, "aborted-commit", context.Background())
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("held transaction is not active")
	}

	// When
	commit := sqliteAttributionStartHeldCommit(t, transaction, session)
	if err := sqliteAttributionTracker.Load().AbortSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	commitErr := <-commit
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)

	// Then
	var holdErr *SQLiteHoldError
	if !errors.As(commitErr, &holdErr) || !errors.Is(commitErr, ErrSQLiteHoldAborted) {
		t.Fatalf("aborted held Commit error = %v", commitErr)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after aborted Commit = %v, want EBADF", descriptorErr)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Fatal("aborted Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Fatal("aborted Commit left the tracker transaction active")
	}
	if count := sqliteAttributionPersistedCount(t, databasePath); count != 0 {
		t.Fatalf("aborted held Commit persisted %d rows", count)
	}
}

func TestSQLiteAttributionCommitRollsBackWhenBeginTxContextCancels(t *testing.T) {
	// Given
	transactionContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, transaction, session, databasePath := sqliteAttributionHeldTransaction(t, "cancelled-commit", transactionContext)
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("cancelled transaction is not active")
	}

	// When
	commit := sqliteAttributionStartHeldCommit(t, transaction, session)
	cancel()
	commitErr := <-commit
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)

	// Then
	var holdErr *SQLiteHoldError
	if !errors.As(commitErr, &holdErr) || !errors.Is(commitErr, ErrSQLiteHoldAborted) || !errors.Is(commitErr, context.Canceled) {
		t.Fatalf("cancelled held Commit error = %v", commitErr)
	}
	if count := sqliteAttributionPersistedCount(t, databasePath); count != 0 {
		t.Fatalf("cancelled held Commit persisted %d rows", count)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after cancelled Commit = %v, want EBADF", descriptorErr)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Fatal("cancelled Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Fatal("cancelled Commit left the tracker transaction active")
	}
}

func TestSQLiteAttributionRollbackWakesSelectedCommit(t *testing.T) {
	// Given
	_, transaction, session, databasePath := sqliteAttributionHeldTransaction(t, "rollback-wakes-commit", context.Background())

	// When
	commit := sqliteAttributionStartHeldCommit(t, transaction, session)
	rollbackErr := transaction.Rollback()
	commitErr := <-commit

	// Then
	if rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	var holdErr *SQLiteHoldError
	if !errors.As(commitErr, &holdErr) || !errors.Is(commitErr, ErrSQLiteHoldAborted) {
		t.Fatalf("Commit error after Rollback = %v", commitErr)
	}
	if count := sqliteAttributionPersistedCount(t, databasePath); count != 0 {
		t.Fatalf("Rollback while Commit waited persisted %d rows", count)
	}
}
