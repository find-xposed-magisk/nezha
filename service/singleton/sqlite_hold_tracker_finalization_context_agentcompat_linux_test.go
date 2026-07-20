//go:build agentcompat && linux

package singleton

import (
	"context"
	"errors"
	"testing"
)

func TestSQLiteHoldTrackerCommitFinalizationCancellationAbortsUnreleasedHold(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(202)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	finalization, err := tracker.BeginSQLiteCommitFinalization(transaction)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	waitErr := tracker.WaitSQLiteCommitFinalization(ctx, finalization)
	releaseErr := tracker.ReleaseSQLiteHold(session)

	// Then
	if !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("cancelled finalization wait error = %v", waitErr)
	}
	if !errors.Is(finalization.Wait(), ErrSQLiteHoldAborted) {
		t.Fatalf("finalization after cancellation = %v", finalization.Wait())
	}
	if !errors.Is(releaseErr, ErrSQLiteHoldStaleSession) {
		t.Fatalf("release after cancellation-owned abort = %v", releaseErr)
	}
}

func TestSQLiteHoldTrackerCommitFinalizationReleaseWinsOverAlreadyCancelledContext(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(203)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	finalization, err := tracker.BeginSQLiteCommitFinalization(transaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	waitErr := tracker.WaitSQLiteCommitFinalization(ctx, finalization)

	// Then
	if waitErr != nil {
		t.Fatalf("released finalization lost to already-cancelled context: %v", waitErr)
	}
}
