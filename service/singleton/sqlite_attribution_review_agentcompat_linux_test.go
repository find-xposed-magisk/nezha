//go:build agentcompat && linux

package singleton

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestSQLiteAttributionLifecycleFieldsUseOneConnectionMutex(t *testing.T) {
	// Given
	mutexType := reflect.TypeFor[sync.Mutex]()
	connectionType := reflect.TypeFor[sqliteAttributionConnection]()
	transactionType := reflect.TypeFor[sqliteAttributionTransaction]()
	mutexCount := 0
	for _, typeUnderTest := range []reflect.Type{connectionType, transactionType} {
		for fieldIndex := 0; fieldIndex < typeUnderTest.NumField(); fieldIndex++ {
			if typeUnderTest.Field(fieldIndex).Type == mutexType {
				mutexCount++
			}
		}
	}

	// When
	field, found := connectionType.FieldByName("lifecycleMu")

	// Then
	if mutexCount != 1 {
		t.Fatalf("direct lifecycle mutex count = %d, want 1 on sqliteAttributionConnection", mutexCount)
	}
	if !found || field.Type != mutexType {
		t.Fatal("sqliteAttributionConnection.lifecycleMu is not the lifecycle mutex")
	}
}

func TestSQLiteAttributionBeginRollsBackRawTransactionWhenTrackerRejects(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	rawConnection, err := sqliteAttributionDriver{}.Open(sqliteAttributionTestDatabasePath(t))
	if err != nil {
		t.Fatal(err)
	}
	connection := rawConnection.(*sqliteAttributionConnection)
	t.Cleanup(func() {
		_, _ = connection.connection.Exec("ROLLBACK", nil)
		if closeErr := connection.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	identity := SQLiteTransaction{Connection: connection.identity, Identity: SQLiteTransactionIdentity(sqliteAttributionTransactionID.Load() + 1)}
	if err := sqliteAttributionTracker.Load().BeginSQLiteTransaction(identity); err != nil {
		t.Fatal(err)
	}

	// When
	_, beginErr := connection.Begin()
	followup, followupErr := connection.Begin()
	if followup != nil {
		_ = followup.Rollback()
	}

	// Then
	if !errors.Is(beginErr, ErrSQLiteHoldTransactionActive) {
		t.Fatalf("tracker rejection = %v, want ErrSQLiteHoldTransactionActive", beginErr)
	}
	if followupErr != nil {
		t.Fatalf("tracker rejection left raw transaction active: %v", followupErr)
	}
}

func TestSQLiteAttributionDirectQueryPreservesSQLiteColumnTypes(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	databasePath := sqliteAttributionTestDatabasePath(t)
	attributed, err := openSQLiteAttributionTestDB(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := attributed.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	standard, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := standard.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := attributed.Exec("CREATE TABLE settings (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := attributed.Exec("INSERT INTO settings (value) VALUES (?)", "typed"); err != nil {
		t.Fatal(err)
	}

	// When
	attributedRows, err := attributed.QueryContext(context.Background(), "SELECT id, value FROM settings")
	if err != nil {
		t.Fatal(err)
	}
	attributedTypes, attributedErr := attributedRows.ColumnTypes()
	attributedCloseErr := attributedRows.Close()
	standardRows, err := standard.QueryContext(context.Background(), "SELECT id, value FROM settings")
	if err != nil {
		t.Fatal(err)
	}
	standardTypes, standardErr := standardRows.ColumnTypes()
	standardCloseErr := standardRows.Close()

	// Then
	if attributedErr != nil || attributedCloseErr != nil || standardErr != nil || standardCloseErr != nil {
		t.Fatal("ColumnTypes did not complete cleanly")
	}
	if len(attributedTypes) != len(standardTypes) {
		t.Fatalf("attributed ColumnTypes length = %d, standard = %d", len(attributedTypes), len(standardTypes))
	}
	for index := range standardTypes {
		if attributedTypes[index].DatabaseTypeName() != standardTypes[index].DatabaseTypeName() || attributedTypes[index].ScanType() != standardTypes[index].ScanType() || !reflect.DeepEqual(attributedTypes[index], standardTypes[index]) {
			t.Fatalf("column %d metadata differs from go-sqlite3", index)
		}
	}
}

func TestSQLiteAttributionFailsClosedWhenSQLiteRepreparesAfterSchemaChange(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	databasePath := sqliteAttributionTestDatabasePath(t)
	rawConnection, err := sqliteAttributionDriver{}.Open(databasePath)
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
	statement, err := connection.Prepare("INSERT INTO settings (value) VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := statement.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	other, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := other.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err := other.Exec("CREATE TABLE audit (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec("CREATE TRIGGER settings_audit AFTER INSERT ON settings BEGIN INSERT INTO audit (value) VALUES (NEW.value); END"); err != nil {
		t.Fatal(err)
	}
	enableSQLiteAttribution()
	transaction, err := connection.Begin()
	if err != nil {
		t.Fatal(err)
	}

	// When
	_, executionErr := statement.Exec([]driver.Value{"reprepared"})
	commitErr := transaction.Commit()
	var settingsCount, auditCount int
	if err := other.QueryRow("SELECT COUNT(*) FROM settings").Scan(&settingsCount); err != nil {
		t.Fatal(err)
	}
	if err := other.QueryRow("SELECT COUNT(*) FROM audit").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}

	// Then
	if !errors.Is(errors.Join(executionErr, commitErr), ErrSQLiteAttributionUnsupportedWrite) {
		t.Fatalf("reprepared write errors = %v / %v", executionErr, commitErr)
	}
	if settingsCount != 0 || auditCount != 0 {
		t.Fatalf("reprepared write persisted settings=%d audit=%d", settingsCount, auditCount)
	}
}

func TestSQLiteAttributionUpdateHookRejectsAuxiliaryDatabase(t *testing.T) {
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
	connection.execution = &sqliteAttributionExecution{classification: sqliteAttributionClassification{operation: SQLiteOperationInsert, table: "settings"}}
	if _, err := connection.connection.Exec("ATTACH DATABASE ':memory:' AS auxiliary", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.connection.Exec("CREATE TABLE auxiliary.settings (value TEXT)", nil); err != nil {
		t.Fatal(err)
	}

	// When
	_, writeErr := connection.connection.Exec("INSERT INTO auxiliary.settings (value) VALUES ('auxiliary')", nil)

	// Then
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if !connection.execution.hook.mismatch || connection.execution.hook.seen {
		t.Fatal("auxiliary database update hook matched main attribution execution")
	}
}
