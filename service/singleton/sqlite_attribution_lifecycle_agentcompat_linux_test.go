//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSQLiteAttributionReturningCompletesExactlyOneRow(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// When
	rows, err := transaction.QueryContext(context.Background(), "INSERT INTO settings (value) VALUES (?) RETURNING id", "one-row")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("RETURNING did not yield its inserted row")
	}
	var identifier int64
	if err := rows.Scan(&identifier); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("RETURNING yielded more than one row")
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	commitErr := transaction.Commit()
	count := sqliteAttributionSettingsCount(t, database)

	// Then
	if rowsErr != nil || closeErr != nil || commitErr != nil {
		t.Fatal("completed RETURNING did not finish successfully")
	}
	if count != 1 {
		t.Fatalf("completed RETURNING persisted %d rows", count)
	}
}

func TestSQLiteAttributionEarlyReturningCloseRollsBackTransaction(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// When
	rows, err := transaction.QueryContext(context.Background(), "INSERT INTO settings (value) VALUES (?) RETURNING id", "early-close")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("RETURNING did not yield its inserted row")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	commitErr := transaction.Commit()
	count := sqliteAttributionSettingsCount(t, database)

	// Then
	if commitErr == nil {
		t.Fatal("early RETURNING Close allowed Commit")
	}
	if count != 0 {
		t.Fatalf("early RETURNING Close persisted %d rows", count)
	}
}

func TestSQLiteAttributionZeroRowUpdateDoesNotPublishEvidence(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// When
	if _, err := transaction.Exec("UPDATE settings SET value = ? WHERE id = ?", "zero", -1); err != nil {
		t.Fatal(err)
	}
	commitErr := transaction.Commit()
	evidence := sqliteAttributionTrackerWriteEvidence()

	// Then
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	if evidence.hasWrite {
		t.Fatal("zero-row UPDATE published write evidence")
	}
}

func TestSQLiteAttributionDirectReadQueryClosesOwnedStatement(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)

	// When
	rows, err := database.QueryContext(context.Background(), "SELECT id FROM settings")
	if err != nil {
		t.Fatal(err)
	}
	next := rows.Next()
	rowsErr := rows.Err()
	closeErr := rows.Close()

	// Then
	if next {
		t.Fatal("empty SELECT returned a row")
	}
	if rowsErr != nil || closeErr != nil {
		t.Fatal("direct readonly Query did not complete cleanly")
	}
}

func TestSQLiteAttributionCommitClosesRetainedJournalDescriptor(t *testing.T) {
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
	create, err := connection.Prepare("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := create.Exec(nil); err != nil {
		t.Fatal(err)
	}
	if err := create.Close(); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	transaction, err := connection.Begin()
	if err != nil {
		t.Fatal(err)
	}
	insert, err := connection.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insert.Exec([]driver.Value{"retained-descriptor"}); err != nil {
		t.Fatal(err)
	}
	if err := insert.Close(); err != nil {
		t.Fatal(err)
	}
	_, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("active transaction is missing before Commit")
	}

	// When
	commitErr := transaction.Commit()
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)

	// Then
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("retained journal descriptor remains open: %v", descriptorErr)
	}
}

func TestSQLiteAttributionQueryRowReturningPoisonsTransaction(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// When
	var identifier int64
	scanErr := transaction.QueryRowContext(context.Background(), "INSERT INTO settings (value) VALUES (?) RETURNING id", "query-row").Scan(&identifier)
	commitErr := transaction.Commit()

	// Then
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	if commitErr == nil {
		t.Fatal("QueryRowContext RETURNING committed without reaching EOF")
	}
	if sqliteAttributionTrackerHasWrite() {
		t.Fatal("QueryRowContext RETURNING published evidence without EOF")
	}
}
