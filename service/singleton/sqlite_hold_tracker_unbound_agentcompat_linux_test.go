//go:build agentcompat && linux

package singleton

import (
	"errors"
	"testing"
)

func TestSQLiteHoldTrackerNextWriterSelectsFutureUpdateAndPublishesSnapshot(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	before, err := tracker.SQLiteHoldSnapshot(session)
	if err != nil {
		t.Fatal(err)
	}
	transaction := sqliteHoldTestTransaction(22)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	if err := tracker.RecordSQLiteExecution(transaction, SQLiteExecutionOrigin{
		Operation: SQLiteOperationInsert, Table: "settings", StackHash: 34, FirstNezhaFrame: "singleton.next",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationInsert, Table: "settings", Journal: sqliteHoldTestJournal,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := tracker.SQLiteHoldSnapshot(session)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if before.SessionID != session.ID() || before.Mode != SQLiteHoldSelectionModeNextWriter || before.Selected {
		t.Fatalf("unexpected unselected snapshot: %+v", before)
	}
	if !after.Selected || after.Transaction != transaction || after.Operation != SQLiteOperationInsert || after.Table != "settings" ||
		after.StackHash != 34 || after.FirstNezhaFrame != "singleton.next" || after.Journal != sqliteHoldTestJournal {
		t.Fatalf("unexpected selected snapshot: %+v", after)
	}
}

func TestSQLiteHoldTrackerNextWriterSelectsOneActiveUpdate(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(23)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)

	// When
	session, err := tracker.ArmNextSQLiteHold()
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if err != nil || snapshotErr != nil {
		t.Fatalf("arm=%v snapshot=%v", err, snapshotErr)
	}
	if !snapshot.Selected || snapshot.Journal != sqliteHoldTestJournal {
		t.Fatalf("unexpected active selection snapshot: %+v", snapshot)
	}
}

func TestSQLiteHoldTrackerNextWriterAbortsMultipleActiveUpdates(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	for _, transaction := range []SQLiteTransaction{sqliteHoldTestTransaction(24), sqliteHoldTestTransaction(25)} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
		recordSQLiteHoldTestUpdate(t, tracker, transaction)
	}

	// When
	_, err := tracker.ArmNextSQLiteHold()

	// Then
	if !errors.Is(err, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("expected ambiguous active updates, got %v", err)
	}
}

func TestSQLiteHoldTrackerNextWriterAbortsDuplicateFutureUpdate(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	first := sqliteHoldTestTransaction(26)
	second := sqliteHoldTestTransaction(27)
	for _, transaction := range []SQLiteTransaction{first, second} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.RecordSQLiteUpdate(first, SQLiteUpdateObservation{
		Operation: SQLiteOperationDelete, Table: "settings", Journal: sqliteHoldTestJournal,
	}); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteUpdate(second, SQLiteUpdateObservation{
		Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal,
	})

	// Then
	if !errors.Is(err, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("expected ambiguous future updates, got %v", err)
	}
	if _, err := tracker.SQLiteHoldSnapshot(session); !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale session after duplicate future update, got %v", err)
	}
}

func TestSQLiteHoldTrackerNextWriterRequiresFinalizationBeforeReleaseAndStalesAfterAbort(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}

	// When
	releaseErr := tracker.ReleaseSQLiteHold(session)
	abortErr := tracker.AbortSQLiteHold(session)

	// Then
	if !errors.Is(releaseErr, ErrSQLiteHoldFinalizationNotStarted) {
		t.Fatalf("expected release rejection, got %v", releaseErr)
	}
	if abortErr != nil {
		t.Fatal(abortErr)
	}
	if _, err := tracker.SQLiteHoldSnapshot(session); !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale aborted session, got %v", err)
	}
}
