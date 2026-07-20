//go:build agentcompat && linux

package singleton

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
	"gorm.io/gorm"
)

func TestSQLiteAttributionHoldFacadeEnablesOnArmAndDisablesOnAbort(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	t.Cleanup(resetSQLiteAttributionForTest)

	// When
	receipt, armErr := ArmNextSQLiteHold()
	enabledAfterArm := sqliteAttributionEnabled.Load()
	_, abortErr := AbortSQLiteHold(receipt)

	// Then
	if armErr != nil || abortErr != nil {
		t.Fatalf("arm=%v abort=%v", armErr, abortErr)
	}
	if !enabledAfterArm {
		t.Fatal("successful hold arm did not enable SQLite attribution")
	}
	if sqliteAttributionEnabled.Load() {
		t.Fatal("successful hold abort left SQLite attribution enabled")
	}
}

func TestSQLiteAttributionHoldFacadeDisablesAfterRelease(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	t.Cleanup(resetSQLiteAttributionForTest)
	receipt, err := ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	transaction := sqliteHoldTestTransaction(204)
	tracker := sqliteAttributionTracker.Load()
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "api_tokens", Journal: sqliteHoldTestJournal}); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	result, releaseErr := ReleaseSQLiteHold(receipt)

	// Then
	if releaseErr != nil || result.State != SQLiteHoldControlStateReleased {
		t.Fatalf("release=%+v err=%v", result, releaseErr)
	}
	if sqliteAttributionEnabled.Load() {
		t.Fatal("successful hold release left SQLite attribution enabled")
	}
}

func TestSQLiteAttributionHoldFacadeKeepsEnabledAfterCanceledWaitUntilAbort(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	t.Cleanup(resetSQLiteAttributionForTest)
	receipt, err := ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, waitErr := WaitSQLiteHoldSelected(ctx, receipt)
	enabledAfterWait := sqliteAttributionEnabled.Load()
	_, abortErr := AbortSQLiteHold(receipt)

	// Then
	if !errors.Is(waitErr, context.Canceled) || abortErr != nil {
		t.Fatalf("wait=%v abort=%v", waitErr, abortErr)
	}
	if !enabledAfterWait {
		t.Fatal("canceled wait disabled attribution before the active hold was aborted")
	}
	if sqliteAttributionEnabled.Load() {
		t.Fatal("abort after canceled wait left SQLite attribution enabled")
	}
}

func TestSQLiteAttributionHoldFacadeStaleReceiptCannotDisableNewHold(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	t.Cleanup(resetSQLiteAttributionForTest)
	first, err := ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AbortSQLiteHold(first); err != nil {
		t.Fatal(err)
	}
	second, err := ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}

	// When
	_, staleErr := AbortSQLiteHold(first)

	// Then
	if !errors.Is(staleErr, ErrSQLiteHoldStaleSession) {
		t.Fatalf("stale abort error = %v", staleErr)
	}
	if !sqliteAttributionEnabled.Load() {
		t.Fatal("stale receipt disabled attribution owned by the current hold")
	}
	if _, err := AbortSQLiteHold(second); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteAttributionHoldFacadeSelectsGORMAPITokenUsageUpdate(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	t.Cleanup(resetSQLiteAttributionForTest)
	database, err := gorm.Open(openSQLiteDialector(filepath.Join(t.TempDir(), "dashboard.sqlite")), &gorm.Config{})
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
	if err := database.AutoMigrate(&model.APIToken{}); err != nil {
		t.Fatal(err)
	}
	token := model.APIToken{UserID: 1, Name: "usage-update", TokenHash: "hash"}
	if err := database.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	receipt, err := ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writerDone := make(chan error, 1)
	usageTime := time.Unix(1_700_000_000, 0)
	go func() {
		writerDone <- database.Model(&model.APIToken{}).Where("id = ?", token.ID).Updates(map[string]any{
			"last_used_at": usageTime,
			"last_used_ip": "127.0.0.1",
		}).Error
		cancel()
	}()

	// When
	selected, selectedErr := WaitSQLiteHoldSelected(ctx, receipt)
	finalizing, finalizingErr := WaitSQLiteHoldFinalizing(ctx, selected)
	_, releaseErr := ReleaseSQLiteHold(finalizing)
	writerErr := <-writerDone

	// Then
	if selectedErr != nil || finalizingErr != nil || releaseErr != nil || writerErr != nil {
		t.Fatalf("selected=%v finalizing=%v release=%v writer=%v", selectedErr, finalizingErr, releaseErr, writerErr)
	}
	var updated model.APIToken
	if err := database.First(&updated, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.LastUsedAt == nil || !updated.LastUsedAt.Equal(usageTime) || updated.LastUsedIP != "127.0.0.1" {
		t.Fatalf("persisted usage update = %+v", updated)
	}
}
