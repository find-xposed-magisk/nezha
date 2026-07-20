//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestSQLiteAttributionRejectsDirectUnboundExecBeforeInsert(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()

	// When
	_, err := database.Exec("INSERT INTO settings (value) VALUES (?)", "direct-unbound")
	count := sqliteAttributionSettingsCount(t, database)

	// Then
	// An UpdateHook error after SQLite executes is too late for autocommit: the write may already be committed.
	if !errors.Is(err, ErrSQLiteAttributionUnboundWrite) {
		t.Error("direct unbound Exec error does not wrap ErrSQLiteAttributionUnboundWrite")
	}
	if count != 0 {
		t.Errorf("direct unbound Exec persisted %d rows", count)
	}
}

func TestSQLiteAttributionRejectsLegacyDriverQueryBeforeInsert(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	connection, err := sqliteAttributionDriver{}.Open(sqliteAttributionTestDatabasePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := connection.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := connection.(driver.Execer).Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()

	// When
	rows, queryErr := connection.(driver.Queryer).Query("INSERT INTO settings (value) VALUES (?) RETURNING id", []driver.Value{"legacy-direct"})
	rowsErr := sqliteAttributionConsumeDriverRows(rows)

	// Then
	if !errors.Is(errors.Join(queryErr, rowsErr), ErrSQLiteAttributionUnboundWrite) {
		t.Error("legacy driver Query does not reject unbound row DML")
	}
}

func TestSQLiteAttributionRejectsPreparedLegacyDriverQueryBeforeInsert(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	connection, err := sqliteAttributionDriver{}.Open(sqliteAttributionTestDatabasePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := connection.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := connection.(driver.Execer).Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	statement, err := connection.Prepare("INSERT INTO settings (value) VALUES (?) RETURNING id")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := statement.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})

	// When
	rows, queryErr := statement.Query([]driver.Value{"legacy-prepared"})
	rowsErr := sqliteAttributionConsumeDriverRows(rows)

	// Then
	if !errors.Is(errors.Join(queryErr, rowsErr), ErrSQLiteAttributionUnboundWrite) {
		t.Error("prepared legacy driver Query does not reject unbound row DML")
	}
}

func TestSQLiteAttributionRejectsPreparedUnboundExecBeforeInsert(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	statement, err := database.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := statement.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})

	// When
	_, err = statement.Exec("prepared-unbound")
	count := sqliteAttributionSettingsCount(t, database)

	// Then
	if !errors.Is(err, ErrSQLiteAttributionUnboundWrite) {
		t.Error("prepared unbound Exec error does not wrap ErrSQLiteAttributionUnboundWrite")
	}
	if count != 0 {
		t.Errorf("prepared unbound Exec persisted %d rows", count)
	}
}

func TestSQLiteAttributionRejectsDirectUnboundReturningBeforeInsert(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()

	// When
	rows, queryErr := database.QueryContext(context.Background(), "INSERT INTO settings (value) VALUES (?) RETURNING id", "direct-returning")
	rowsErr := sqliteAttributionConsumeRows(rows)
	count := sqliteAttributionSettingsCount(t, database)

	// Then
	if !errors.Is(errors.Join(queryErr, rowsErr), ErrSQLiteAttributionUnboundWrite) {
		t.Error("direct unbound RETURNING does not propagate ErrSQLiteAttributionUnboundWrite through rows")
	}
	if count != 0 {
		t.Errorf("direct unbound RETURNING persisted %d rows", count)
	}
}

func TestSQLiteAttributionRejectsPreparedUnboundReturningBeforeInsert(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	statement, err := database.PrepareContext(context.Background(), "INSERT INTO settings (value) VALUES (?) RETURNING id")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := statement.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})

	// When
	rows, queryErr := statement.QueryContext(context.Background(), "prepared-returning")
	rowsErr := sqliteAttributionConsumeRows(rows)
	count := sqliteAttributionSettingsCount(t, database)

	// Then
	if !errors.Is(errors.Join(queryErr, rowsErr), ErrSQLiteAttributionUnboundWrite) {
		t.Error("prepared unbound RETURNING does not propagate ErrSQLiteAttributionUnboundWrite through rows")
	}
	if count != 0 {
		t.Errorf("prepared unbound RETURNING persisted %d rows", count)
	}
}

func TestSQLiteAttributionRejectsDirectUnboundUpdateBeforeSideEffect(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	if _, err := database.Exec("INSERT INTO settings (value) VALUES (?)", "original"); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()

	// When
	_, err := database.Exec("UPDATE settings SET value = ?", "changed")
	value := sqliteAttributionSettingValue(t, database)

	// Then
	if !errors.Is(err, ErrSQLiteAttributionUnboundWrite) {
		t.Error("direct unbound UPDATE error does not wrap ErrSQLiteAttributionUnboundWrite")
	}
	if value != "original" {
		t.Error("direct unbound UPDATE changed the persisted value")
	}
}

func TestSQLiteAttributionRejectsPreparedUnboundDeleteBeforeSideEffect(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	if _, err := database.Exec("INSERT INTO settings (value) VALUES (?)", "preserved"); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	statement, err := database.Prepare("DELETE FROM settings")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := statement.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})

	// When
	_, err = statement.Exec()
	count := sqliteAttributionSettingsCount(t, database)

	// Then
	if !errors.Is(err, ErrSQLiteAttributionUnboundWrite) {
		t.Error("prepared unbound DELETE error does not wrap ErrSQLiteAttributionUnboundWrite")
	}
	if count != 1 {
		t.Errorf("prepared unbound DELETE left %d rows", count)
	}
}
