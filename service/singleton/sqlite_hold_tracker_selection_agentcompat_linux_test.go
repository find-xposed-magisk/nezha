//go:build agentcompat && linux

package singleton

import (
	"errors"
	"testing"
)

func TestSQLiteHoldTrackerAbortsSessionWhenFutureDuplicateArrives(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransaction(12)
	if err := tracker.BeginSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, first)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	second := sqliteHoldTestTransaction(13)
	if err := tracker.BeginSQLiteTransaction(second); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteUpdate(second, SQLiteUpdateObservation{
		Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal,
	})

	// Then
	if !errors.Is(err, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("expected ambiguous candidate error, got %v", err)
	}
	if err := tracker.ReleaseSQLiteHold(session); !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale session after duplicate candidate, got %v", err)
	}
	if _, err := tracker.BeginSQLiteFinalization(first); !errors.Is(err, ErrSQLiteHoldNotSelected) {
		t.Fatalf("expected aborted session, got %v", err)
	}
}

func TestSQLiteHoldTrackerRejectsRepeatedRelease(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(21)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.ReleaseSQLiteHold(session)

	// Then
	if !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale repeated release, got %v", err)
	}
}

func TestSQLiteHoldTrackerRejectsReleaseBeforeFinalization(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.ReleaseSQLiteHold(session)

	// Then
	if !errors.Is(err, ErrSQLiteHoldFinalizationNotStarted) {
		t.Fatalf("expected finalization-not-started error, got %v", err)
	}
}

func TestSQLiteHoldTrackerRejectsAbortAfterRelease(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(14)
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
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.AbortSQLiteHold(session)

	// Then
	if !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale session error, got %v", err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerIgnoresFutureCandidateAfterRelease(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransaction(15)
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
	second := sqliteHoldTestTransaction(16)
	if err := tracker.BeginSQLiteTransaction(second); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteUpdate(second, SQLiteUpdateObservation{
		Operation: SQLiteOperationUpdate, Table: "settings", Journal: sqliteHoldTestJournal,
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerDoesNotMatchJournalWithDifferentMountOrBirthTime(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	journal := SQLiteJournalIdentity{MountID: 2, DeviceMajor: 8, DeviceMinor: 1, Inode: 13, BirthSeconds: 10, BirthNanoseconds: 20}
	transaction := sqliteHoldTestTransaction(17)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationInsert, Table: "settings", Journal: SQLiteJournalIdentity{
			MountID: 3, DeviceMajor: 8, DeviceMinor: 1, Inode: 13, BirthSeconds: 10, BirthNanoseconds: 20,
		},
	}); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmSQLiteHold(journal)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); !errors.Is(err, ErrSQLiteHoldNotSelected) {
		t.Fatalf("expected mount mismatch not to select, got %v", err)
	}
	if err := tracker.AbortSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerDoesNotMatchJournalWithDifferentBirthTime(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	journal := SQLiteJournalIdentity{MountID: 2, DeviceMajor: 8, DeviceMinor: 1, Inode: 13, BirthSeconds: 10, BirthNanoseconds: 20}
	transaction := sqliteHoldTestTransaction(18)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationDelete, Table: "settings", Journal: SQLiteJournalIdentity{
			MountID: 2, DeviceMajor: 8, DeviceMinor: 1, Inode: 13, BirthSeconds: 10, BirthNanoseconds: 21,
		},
	}); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmSQLiteHold(journal)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); !errors.Is(err, ErrSQLiteHoldNotSelected) {
		t.Fatalf("expected birth-time mismatch not to select, got %v", err)
	}
	if err := tracker.AbortSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerRejectsDuplicateTransactionIdentity(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(19)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	err := tracker.BeginSQLiteTransaction(transaction)

	// Then
	if !errors.Is(err, ErrSQLiteHoldTransactionActive) {
		t.Fatalf("expected transaction-active error, got %v", err)
	}
}

func TestSQLiteHoldTrackerSelectsDeleteOperation(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(20)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationDelete, Table: "settings", Journal: sqliteHoldTestJournal,
	}); err != nil {
		t.Fatal(err)
	}

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
