//go:build agentcompat && linux

package singleton

import (
	"errors"
	"testing"
)

var sqliteHoldTestJournal = SQLiteJournalIdentity{
	MountID: 2, DeviceMajor: 8, DeviceMinor: 1, Inode: 13, BirthSeconds: 10, BirthNanoseconds: 20,
}

func sqliteHoldTestTransaction(id SQLiteTransactionIdentity) SQLiteTransaction {
	return SQLiteTransaction{Connection: SQLiteConnectionIdentity(3), Identity: id}
}

func recordSQLiteHoldTestUpdate(t *testing.T, tracker *SQLiteHoldTracker, transaction SQLiteTransaction) {
	t.Helper()
	if err := tracker.RecordSQLiteExecution(transaction, SQLiteExecutionOrigin{
		Operation: SQLiteOperationUpdate, Table: "settings", StackHash: 21, FirstNezhaFrame: "singleton.test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerSelectsFutureCandidateAfterZeroCandidateArm(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	transaction := sqliteHoldTestTransaction(1)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	finalization, err := tracker.BeginSQLiteFinalization(transaction)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerSelectsSingleActiveCandidate(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(2)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)

	// When
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	finalization, finalizationErr := tracker.BeginSQLiteFinalization(transaction)

	// Then
	if err != nil || finalizationErr != nil {
		t.Fatalf("arm=%v finalization=%v", err, finalizationErr)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerAbortsDuplicateCandidates(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransaction(3)
	second := sqliteHoldTestTransaction(4)
	for _, transaction := range []SQLiteTransaction{first, second} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
		recordSQLiteHoldTestUpdate(t, tracker, transaction)
	}

	// When
	_, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)

	// Then
	if !errors.Is(err, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("expected ambiguous candidate error, got %v", err)
	}
	if _, err := tracker.BeginSQLiteFinalization(first); !errors.Is(err, ErrSQLiteHoldNotSelected) {
		t.Fatalf("expected no selected transaction, got %v", err)
	}
}

func TestSQLiteHoldTrackerStaleReleaseCannotReleaseNewSession(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransaction(5)
	if err := tracker.BeginSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, first)
	staleSession, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.AbortSQLiteHold(staleSession); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	second := sqliteHoldTestTransaction(6)
	if err := tracker.BeginSQLiteTransaction(second); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, second)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	finalization, err := tracker.BeginSQLiteFinalization(second)
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.ReleaseSQLiteHold(staleSession)

	// Then
	if !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale session error, got %v", err)
	}
	if finalization.Released() {
		t.Fatal("stale release released the new session")
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerLinearizesArmBeforeFinalization(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(7)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}

	// When
	finalization, err := tracker.BeginSQLiteFinalization(transaction)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if finalization.Released() {
		t.Fatal("finalization released before its session release")
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerRollbackNeverWaits(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(8)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	finalization, err := tracker.BeginSQLiteFinalization(transaction)
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.FinishSQLiteTransaction(transaction)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); !errors.Is(err, ErrSQLiteHoldAborted) {
		t.Fatalf("expected aborted finalization, got %v", err)
	}
	if err := tracker.ReleaseSQLiteHold(session); !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale release, got %v", err)
	}
}

func TestSQLiteHoldTrackerAbortUnblocksSelectedFinalization(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(9)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	finalization, err := tracker.BeginSQLiteFinalization(transaction)
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.AbortSQLiteHold(session)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); !errors.Is(err, ErrSQLiteHoldAborted) {
		t.Fatalf("expected aborted finalization, got %v", err)
	}
}

func TestSQLiteHoldTrackerCleansUpAfterReleasedFinalization(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransaction(10)
	if err := tracker.BeginSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, first)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	finalization, err := tracker.BeginSQLiteFinalization(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	second := sqliteHoldTestTransaction(11)
	if err := tracker.BeginSQLiteTransaction(second); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, second)

	// When
	_, err = tracker.ArmSQLiteHold(sqliteHoldTestJournal)

	// Then
	if err != nil {
		t.Fatal(err)
	}
}
