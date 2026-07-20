//go:build agentcompat && linux

package singleton

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
)

func TestSQLiteDriverAdapterRecordsExplicitInsert_when_AttributionEnabled(t *testing.T) {
	resetSQLiteAttributionForTest()
	databasePath := filepath.Join(t.TempDir(), "dashboard.sqlite")
	database, err := openSQLiteAttributionTestDB(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := database.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec("INSERT INTO settings (value) VALUES (?)", "opaque"); err != nil {
		t.Fatal(err)
	}

	if !sqliteAttributionTrackerHasWrite() {
		t.Fatal("explicit insert did not record an atomic sqlite write")
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteDriverAdapterRecordsPreparedExplicitInsert_when_AttributionEnabled(t *testing.T) {
	resetSQLiteAttributionForTest()
	databasePath := filepath.Join(t.TempDir(), "dashboard.sqlite")
	database, err := openSQLiteAttributionTestDB(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := database.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := transaction.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := statement.Exec("prepared"); err != nil {
		t.Fatal(err)
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	if !sqliteAttributionTrackerHasWrite() {
		t.Fatal("prepared explicit insert did not record an atomic sqlite write")
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteDriverAdapterRejectsUnboundWrite_when_AttributionEnabled(t *testing.T) {
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	_, err := database.Exec("INSERT INTO settings (value) VALUES (?)", "unbound")
	if !errors.Is(err, ErrSQLiteAttributionUnboundWrite) {
		t.Fatalf("expected unbound write instrumentation error, got %v", err)
	}
}

func TestSQLiteDriverAdapterRejectsUnsupportedDSN(t *testing.T) {
	database, err := openSQLiteAttributionTestDB(":memory:")
	if err == nil {
		err = database.Ping()
	}
	if database != nil {
		t.Cleanup(func() {
			if closeErr := database.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
	}
	if !errors.Is(err, ErrSQLiteAttributionUnsupportedDSN) {
		t.Fatalf("expected unsupported DSN error, got %v", err)
	}
}

func openSQLiteAttributionTestDB(path string) (*sql.DB, error) {
	registerSQLiteAttributionDriver()
	return sql.Open(sqliteAttributionDriverName, path)
}

func openSQLiteAttributionTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	resetSQLiteAttributionForTest()
	database, err := openSQLiteAttributionTestDB(sqliteAttributionTestDatabasePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := database.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	return database
}

func sqliteAttributionTestDatabasePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "dashboard.sqlite")
}

func sqliteAttributionSettingsCount(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM settings").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func sqliteAttributionSettingValue(t *testing.T, database *sql.DB) string {
	t.Helper()
	var value string
	if err := database.QueryRow("SELECT value FROM settings LIMIT 1").Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func sqliteAttributionConsumeRows(rows *sql.Rows) error {
	if rows == nil {
		return nil
	}
	var identifier int64
	for rows.Next() {
		if err := rows.Scan(&identifier); err != nil {
			closeErr := rows.Close()
			return errors.Join(err, closeErr)
		}
	}
	err := rows.Err()
	closeErr := rows.Close()
	return errors.Join(err, closeErr)
}

func sqliteAttributionConsumeDriverRows(rows driver.Rows) error {
	if rows == nil {
		return nil
	}
	values := make([]driver.Value, len(rows.Columns()))
	for {
		err := rows.Next(values)
		if err != nil {
			closeErr := rows.Close()
			return errors.Join(err, closeErr)
		}
	}
}

func sqliteAttributionTrackerHasWrite() bool {
	return sqliteAttributionTrackerWriteEvidence().hasWrite
}

type sqliteAttributionWriteEvidence struct {
	hasWrite bool
	write    SQLiteWriteObservation
}

func sqliteAttributionTrackerWriteEvidence() sqliteAttributionWriteEvidence {
	tracker := sqliteAttributionTracker.Load()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	for _, held := range tracker.transactions {
		if held.hasWrite {
			return sqliteAttributionWriteEvidence{hasWrite: true, write: held.write}
		}
	}
	return sqliteAttributionWriteEvidence{}
}
