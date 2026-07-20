//go:build agentcompat && linux

package singleton

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("NEZHA_SQLITE_HOLD_INVALID_TARGET_HELPER") == "1" {
		tracker := NewSQLiteHoldTracker()
		session, err := tracker.ArmNextSQLiteHold()
		if err != nil {
			os.Exit(2)
		}
		_, waitErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitTarget(99))
		if !errors.Is(waitErr, ErrSQLiteHoldInvalidWaitTarget) {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSQLiteHoldTrackerWaitRejectsInvalidTargetImmediately(t *testing.T) {

	// Given
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteHoldTrackerWaitRejectsInvalidTargetImmediately$")
	command.Env = append(os.Environ(), "NEZHA_SQLITE_HOLD_INVALID_TARGET_HELPER=1")

	// When
	err := command.Run()

	// Then
	if err != nil {
		t.Fatalf("invalid target helper did not return before watchdog deadline: %v", err)
	}
}

func TestSQLiteHoldTrackerRecordsAmbiguityForEveryFutureConflictCandidate(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	_, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	first, second, mismatch := sqliteHoldTestTransaction(81), sqliteHoldTestTransaction(82), sqliteHoldTestTransaction(83)
	for _, transaction := range []SQLiteTransaction{first, second, mismatch} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.RecordSQLiteUpdate(first, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(mismatch, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: SQLiteJournalIdentity{Inode: 84}}); err != nil {
		t.Fatal(err)
	}

	// When
	duplicateErr := tracker.RecordSQLiteUpdate(second, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal})
	_, firstErr := tracker.BeginSQLiteCommitFinalization(first)
	_, secondErr := tracker.BeginSQLiteCommitFinalization(second)
	_, mismatchErr := tracker.BeginSQLiteCommitFinalization(mismatch)

	// Then
	if !errors.Is(duplicateErr, ErrSQLiteHoldAmbiguousCandidate) || !errors.Is(firstErr, ErrSQLiteHoldAmbiguousCandidate) || !errors.Is(secondErr, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("duplicate=%v first=%v second=%v", duplicateErr, firstErr, secondErr)
	}
	if !errors.Is(mismatchErr, ErrSQLiteHoldNotSelected) {
		t.Fatalf("mismatched transaction cause = %v", mismatchErr)
	}
}

func TestSQLiteHoldTrackerRecordsAmbiguityForEveryArmTimeConflictCandidate(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first, second, mismatch := sqliteHoldTestTransaction(85), sqliteHoldTestTransaction(86), sqliteHoldTestTransaction(87)
	for _, transaction := range []SQLiteTransaction{first, second, mismatch} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
	}
	for _, transaction := range []SQLiteTransaction{first, second} {
		if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.RecordSQLiteUpdate(mismatch, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: SQLiteJournalIdentity{Inode: 88}}); err != nil {
		t.Fatal(err)
	}

	// When
	_, armErr := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	_, firstErr := tracker.BeginSQLiteCommitFinalization(first)
	_, secondErr := tracker.BeginSQLiteCommitFinalization(second)
	_, mismatchErr := tracker.BeginSQLiteCommitFinalization(mismatch)

	// Then
	if !errors.Is(armErr, ErrSQLiteHoldAmbiguousCandidate) || !errors.Is(firstErr, ErrSQLiteHoldAmbiguousCandidate) || !errors.Is(secondErr, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("arm=%v first=%v second=%v", armErr, firstErr, secondErr)
	}
	if !errors.Is(mismatchErr, ErrSQLiteHoldNotSelected) {
		t.Fatalf("mismatched transaction cause = %v", mismatchErr)
	}
}

func TestSQLiteHoldTrackerFinishDeletesAmbiguityCause(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first, second := sqliteHoldTestTransaction(89), sqliteHoldTestTransaction(90)
	for _, transaction := range []SQLiteTransaction{first, second} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
		if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal); !errors.Is(err, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatal(err)
	}

	// When
	if err := tracker.FinishSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	_, lookupErr := tracker.BeginSQLiteCommitFinalization(first)

	// Then
	if !errors.Is(lookupErr, ErrSQLiteHoldNotSelected) {
		t.Fatalf("finished transaction ambiguity cause = %v", lookupErr)
	}
}
