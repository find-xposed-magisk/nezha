//go:build agentcompat && linux

package singleton

import (
	"context"
	"errors"
	"testing"
)

func TestSQLiteHoldTrackerWaitSelectedAndFinalizingObserveTransitionsWithoutLostWakeup(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	transaction := sqliteHoldTestTransaction(61)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)

	// When
	selected, selectedErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitSelected)
	if _, err := tracker.BeginSQLiteFinalization(transaction); err != nil {
		t.Fatal(err)
	}
	finalizing, finalizingErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)

	// Then
	if selectedErr != nil || !selected.Selected {
		t.Fatalf("selected=%+v err=%v", selected, selectedErr)
	}
	if finalizingErr != nil || !finalizing.Finalizing {
		t.Fatalf("finalizing=%+v err=%v", finalizing, finalizingErr)
	}
}

func TestSQLiteHoldTrackerWaitCancellationLeavesSessionActive(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, waitErr := tracker.WaitSQLiteHold(ctx, session, SQLiteHoldWaitSelected)
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("wait error = %v, want context cancellation", waitErr)
	}
	if snapshotErr != nil || snapshot.Selected {
		t.Fatalf("snapshot=%+v err=%v", snapshot, snapshotErr)
	}
}

func TestSQLiteHoldTrackerWaitReturnsTerminalAbortAndAmbiguityCauses(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	abortResult := make(chan error, 1)
	go func() {
		_, waitErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitSelected)
		abortResult <- waitErr
	}()

	// When
	if err := tracker.AbortSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	abortErr := <-abortResult
	if _, err := tracker.ArmNextSQLiteHold(); err != nil {
		t.Fatal(err)
	}
	first, second := sqliteHoldTestTransaction(62), sqliteHoldTestTransaction(63)
	for _, transaction := range []SQLiteTransaction{first, second} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.RecordSQLiteUpdate(first, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal}); err != nil {
		t.Fatal(err)
	}
	ambiguityErr := tracker.RecordSQLiteUpdate(second, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal})

	// Then
	if !errors.Is(abortErr, ErrSQLiteHoldAborted) {
		t.Fatalf("abort waiter error = %v", abortErr)
	}
	if !errors.Is(ambiguityErr, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("ambiguity error = %v", ambiguityErr)
	}
	if _, lookupErr := tracker.BeginSQLiteCommitFinalization(first); !errors.Is(lookupErr, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("ambiguity lookup error = %v", lookupErr)
	}
}

func TestSQLiteHoldTrackerCommitFinalizationReturnsSelectedAbortCause(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(64)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.AbortSQLiteHold(session); err != nil {
		t.Fatal(err)
	}

	// When
	_, selectedErr := tracker.BeginSQLiteCommitFinalization(transaction)
	_, unselectedErr := tracker.BeginSQLiteCommitFinalization(sqliteHoldTestTransaction(65))

	// Then
	if !errors.Is(selectedErr, ErrSQLiteHoldAborted) {
		t.Fatalf("selected aborted lookup error = %v", selectedErr)
	}
	if !errors.Is(unselectedErr, ErrSQLiteHoldNotSelected) {
		t.Fatalf("unselected lookup error = %v", unselectedErr)
	}
}

func TestSQLiteHoldTrackerCommitFinalizationPreservesCauseAcrossLaterLifecycle(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransaction(70)
	if err := tracker.BeginSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, first)
	firstSession, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.AbortSQLiteHold(firstSession); err != nil {
		t.Fatal(err)
	}
	second := sqliteHoldTestTransaction(71)
	if err := tracker.BeginSQLiteTransaction(second); err != nil {
		t.Fatal(err)
	}
	secondJournal := SQLiteJournalIdentity{Inode: 72}
	if err := tracker.RecordSQLiteUpdate(second, SQLiteUpdateObservation{Operation: SQLiteOperationUpdate, Table: "settings", Journal: secondJournal}); err != nil {
		t.Fatal(err)
	}
	secondSession, err := tracker.ArmSQLiteHold(secondJournal)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.AbortSQLiteHold(secondSession); err != nil {
		t.Fatal(err)
	}

	// When
	_, lookupErr := tracker.BeginSQLiteCommitFinalization(first)

	// Then
	if !errors.Is(lookupErr, ErrSQLiteHoldAborted) {
		t.Fatalf("first lifecycle abort cause after second lifecycle = %v", lookupErr)
	}
}

func TestSQLiteHoldTrackerWaitSelectedReturnsAbortWhenSelectedTransactionFinishes(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(68)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, waitErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
		result <- waitErr
	}()

	// When
	if err := tracker.FinishSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// Then
	if waitErr := <-result; !errors.Is(waitErr, ErrSQLiteHoldAborted) {
		t.Fatalf("finished selected transaction wait error = %v", waitErr)
	}
}

func TestSQLiteHoldTrackerWaitTreatsFinishedReleasedSessionAsSelectedAndFinalizing(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(69)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	selected, selectedErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitSelected)
	finalizing, finalizingErr := tracker.WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)

	// Then
	if selectedErr != nil || !selected.Released || !selected.Selected {
		t.Fatalf("selected=%+v err=%v", selected, selectedErr)
	}
	if finalizingErr != nil || !finalizing.Released || !finalizing.Finalizing {
		t.Fatalf("finalizing=%+v err=%v", finalizing, finalizingErr)
	}
}
