//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func sqliteAttributionTransactionWithWrite(t *testing.T, databasePath, value string) (*sqliteAttributionConnection, driver.Tx) {
	t.Helper()
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
	return connection, transaction
}

func sqliteAttributionTransactionWithAmbiguousWrite(t *testing.T, databasePath, value string) (*sqliteAttributionConnection, driver.Tx, error) {
	t.Helper()
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
	transaction, err := connection.BeginTx(context.Background(), driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	statement, err := connection.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := statement.Exec([]driver.Value{value})
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	return connection, transaction, writeErr
}

func TestSQLiteAttributionUnselectedCommitPersistsImmediately(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	enableSQLiteAttribution()
	databasePath := sqliteAttributionTestDatabasePath(t)
	session, err := sqliteAttributionTracker.Load().ArmSQLiteHold(SQLiteJournalIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	connection, transaction := sqliteAttributionTransactionWithWrite(t, databasePath, "unselected-commit")
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("unselected transaction is not active")
	}

	// When
	commitErr := transaction.Commit()
	abortErr := sqliteAttributionTracker.Load().AbortSQLiteHold(session)
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)

	// Then
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	if abortErr != nil {
		t.Fatal(abortErr)
	}
	if count := sqliteAttributionPersistedCount(t, databasePath); count != 1 {
		t.Fatalf("unselected Commit persisted %d rows", count)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after unselected Commit = %v, want EBADF", descriptorErr)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Fatal("unselected Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Fatal("unselected Commit left the tracker transaction active")
	}
}

func TestSQLiteAttributionFutureAmbiguityRollsBackEveryParticipatingCommit(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	enableSQLiteAttribution()
	firstPath := sqliteAttributionTestDatabasePath(t)
	secondPath := sqliteAttributionTestDatabasePath(t)
	tracker := sqliteAttributionTracker.Load()
	if _, err := tracker.ArmNextSQLiteHold(); err != nil {
		t.Fatal(err)
	}
	firstConnection, firstTransaction := sqliteAttributionTransactionWithWrite(t, firstPath, "first-ambiguous")
	secondConnection, secondTransaction, secondWriteErr := sqliteAttributionTransactionWithAmbiguousWrite(t, secondPath, "second-ambiguous")
	if !errors.Is(secondWriteErr, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("second ambiguity write error = %v", secondWriteErr)
	}
	firstIdentity, _, firstActive := sqliteAttributionTransactionState(t, firstConnection)
	secondIdentity, _, secondActive := sqliteAttributionTransactionState(t, secondConnection)
	if !firstActive || !secondActive {
		t.Fatal("ambiguity participants are not active")
	}

	// When
	firstCommitErr := firstTransaction.Commit()
	secondCommitErr := secondTransaction.Commit()

	// Then
	for _, commitErr := range []error{firstCommitErr, secondCommitErr} {
		var holdErr *SQLiteHoldError
		if !errors.As(commitErr, &holdErr) || !errors.Is(commitErr, ErrSQLiteHoldAmbiguousCandidate) {
			t.Fatalf("ambiguous Commit error = %v", commitErr)
		}
	}
	if count := sqliteAttributionPersistedCount(t, firstPath); count != 0 {
		t.Fatalf("first ambiguous Commit persisted %d rows", count)
	}
	if count := sqliteAttributionPersistedCount(t, secondPath); count != 0 {
		t.Fatalf("second ambiguous Commit persisted %d rows", count)
	}
	if sqliteAttributionTrackerTransactionActive(tracker, firstIdentity) || sqliteAttributionTrackerTransactionActive(tracker, secondIdentity) {
		t.Fatal("ambiguous Commit left a tracker transaction active")
	}
}
