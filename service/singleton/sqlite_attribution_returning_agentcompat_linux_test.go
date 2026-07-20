//go:build agentcompat && linux

package singleton

import (
	"context"
	"testing"

	"github.com/nezhahq/nezha/model"
	"gorm.io/gorm"
)

func TestSQLiteAttributionRecordsDirectExplicitReturningEvidence(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if rollbackErr := transaction.Rollback(); rollbackErr != nil {
			t.Error(rollbackErr)
		}
	})

	// When
	rows, queryErr := transaction.QueryContext(context.Background(), "INSERT INTO settings (value) VALUES (?) RETURNING id", "direct-explicit-returning")
	rowsErr := sqliteAttributionConsumeRows(rows)
	evidence := sqliteAttributionTrackerWriteEvidence()

	// Then
	if queryErr != nil || rowsErr != nil {
		t.Fatalf("direct explicit RETURNING failed")
	}
	if !evidence.hasWrite {
		t.Fatal("direct explicit RETURNING did not record atomic write evidence")
	}
	if evidence.write.Origin.StackHash == 0 {
		t.Fatal("direct explicit RETURNING recorded a zero stack hash")
	}
	if evidence.write.Origin.FirstNezhaFrame == "" {
		t.Fatal("direct explicit RETURNING recorded an empty Nezha frame")
	}
}

func TestSQLiteAttributionRecordsPreparedExplicitReturningEvidence(t *testing.T) {
	// Given
	database := openSQLiteAttributionTestDatabase(t)
	enableSQLiteAttribution()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if rollbackErr := transaction.Rollback(); rollbackErr != nil {
			t.Error(rollbackErr)
		}
	})
	statement, err := transaction.PrepareContext(context.Background(), "INSERT INTO settings (value) VALUES (?) RETURNING id")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := statement.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})

	// When
	rows, queryErr := statement.QueryContext(context.Background(), "prepared-explicit-returning")
	rowsErr := sqliteAttributionConsumeRows(rows)
	evidence := sqliteAttributionTrackerWriteEvidence()

	// Then
	if queryErr != nil || rowsErr != nil {
		t.Fatalf("prepared explicit RETURNING failed")
	}
	if !evidence.hasWrite {
		t.Fatal("prepared explicit RETURNING did not record atomic write evidence")
	}
	if evidence.write.Origin.StackHash == 0 {
		t.Fatal("prepared explicit RETURNING recorded a zero stack hash")
	}
	if evidence.write.Origin.FirstNezhaFrame == "" {
		t.Fatal("prepared explicit RETURNING recorded an empty Nezha frame")
	}
}

func TestSQLiteAttributionAllowsGORMReturningWhenDisabled(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	database, err := gorm.Open(openSQLiteDialector(sqliteAttributionTestDatabasePath(t)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDatabase.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if err := database.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	user := model.User{Username: "admin", Password: "hashed-password"}

	// When
	createErr := database.Create(&user).Error

	// Then
	if createErr != nil {
		t.Fatalf("disabled attribution rejected GORM RETURNING: %v", createErr)
	}
	if user.ID == 0 {
		t.Fatal("disabled attribution did not return the generated user ID")
	}
}
