//go:build agentcompat && linux

package singleton

import (
	"errors"
	"testing"
)

func sqliteHoldTestWrite(operation SQLiteOperation, table string, stackHash uint64, journal SQLiteJournalIdentity) SQLiteWriteObservation {
	return SQLiteWriteObservation{
		Origin: SQLiteExecutionOrigin{Operation: operation, Table: table, StackHash: stackHash, FirstNezhaFrame: "singleton.write"},
		Update: SQLiteUpdateObservation{Operation: operation, Table: table, Journal: journal},
	}
}

func TestSQLiteHoldTrackerRecordsAtomicFirstWriteAttribution(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(41)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteWrite(transaction, sqliteHoldTestWrite(SQLiteOperationInsert, "settings", 51, sqliteHoldTestJournal))
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if err != nil || snapshotErr != nil {
		t.Fatalf("write=%v snapshot=%v", err, snapshotErr)
	}
	if !snapshot.Selected || snapshot.Transaction != transaction || snapshot.Operation != SQLiteOperationInsert ||
		snapshot.Table != "settings" || snapshot.StackHash != 51 || snapshot.FirstNezhaFrame != "singleton.write" ||
		snapshot.Journal != sqliteHoldTestJournal {
		t.Fatalf("unexpected atomic snapshot: %+v", snapshot)
	}
}

func TestSQLiteHoldTrackerPreservesFirstWriteForRepeatedSameTransaction(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(42)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteWrite(transaction, sqliteHoldTestWrite(SQLiteOperationUpdate, "first", 52, sqliteHoldTestJournal)); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteWrite(transaction, sqliteHoldTestWrite(SQLiteOperationDelete, "later", 53, SQLiteJournalIdentity{Inode: 98}))
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if err != nil || snapshotErr != nil {
		t.Fatalf("repeat=%v snapshot=%v", err, snapshotErr)
	}
	if snapshot.Operation != SQLiteOperationUpdate || snapshot.Table != "first" || snapshot.StackHash != 52 || snapshot.Journal != sqliteHoldTestJournal {
		t.Fatalf("repeated callback changed first write: %+v", snapshot)
	}
}

func TestSQLiteHoldTrackerAbortsDifferentAtomicWriterBeforeRelease(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	first, second := sqliteHoldTestTransaction(43), sqliteHoldTestTransaction(44)
	for _, transaction := range []SQLiteTransaction{first, second} {
		if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.RecordSQLiteWrite(first, sqliteHoldTestWrite(SQLiteOperationInsert, "first", 54, sqliteHoldTestJournal)); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteWrite(second, sqliteHoldTestWrite(SQLiteOperationDelete, "second", 55, sqliteHoldTestJournal))

	// Then
	if !errors.Is(err, ErrSQLiteHoldAmbiguousCandidate) {
		t.Fatalf("expected ambiguity, got %v", err)
	}
	if _, err := tracker.SQLiteHoldSnapshot(session); !errors.Is(err, ErrSQLiteHoldStaleSession) {
		t.Fatalf("expected stale aborted session, got %v", err)
	}
}

func TestSQLiteHoldTrackerKnownJournalMakesFirstMismatchedWriteIneligible(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(45)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteWrite(transaction, sqliteHoldTestWrite(SQLiteOperationUpdate, "wrong", 56, SQLiteJournalIdentity{Inode: 97})); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteWrite(transaction, sqliteHoldTestWrite(SQLiteOperationUpdate, "matching", 57, sqliteHoldTestJournal))
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if err != nil || snapshotErr != nil {
		t.Fatalf("later write=%v snapshot=%v", err, snapshotErr)
	}
	if snapshot.Selected || snapshot.Journal != sqliteHoldTestJournal {
		t.Fatalf("mismatched first write selected session: %+v", snapshot)
	}
}

func TestSQLiteHoldTrackerRejectsAtomicWriteAfterFinalization(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(46)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	first := sqliteHoldTestWrite(SQLiteOperationInsert, "first", 58, sqliteHoldTestJournal)
	if err := tracker.RecordSQLiteWrite(transaction, first); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	err = tracker.RecordSQLiteWrite(transaction, sqliteHoldTestWrite(SQLiteOperationDelete, "later", 59, SQLiteJournalIdentity{Inode: 96}))
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if !errors.Is(err, ErrSQLiteHoldFinalizationStarted) || snapshotErr != nil {
		t.Fatalf("write=%v snapshot=%v", err, snapshotErr)
	}
	if snapshot.Operation != first.Update.Operation || snapshot.Table != first.Update.Table || snapshot.StackHash != first.Origin.StackHash || snapshot.Journal != first.Update.Journal {
		t.Fatalf("finalizing atomic write changed snapshot: %+v", snapshot)
	}
}

func TestSQLiteHoldTrackerRejectsLegacyWritesAfterAtomicFirstWrite(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(47)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	journalA := sqliteHoldTestJournal
	journalB := SQLiteJournalIdentity{Inode: 95}
	session, err := tracker.ArmSQLiteHold(journalA)
	if err != nil {
		t.Fatal(err)
	}
	first := sqliteHoldTestWrite(SQLiteOperationInsert, "atomic", 60, journalB)
	if err := tracker.RecordSQLiteWrite(transaction, first); err != nil {
		t.Fatal(err)
	}

	// When
	executionErr := tracker.RecordSQLiteExecution(transaction, SQLiteExecutionOrigin{
		Operation: SQLiteOperationUpdate, Table: "legacy", StackHash: 61, FirstNezhaFrame: "singleton.legacy",
	})
	updateErr := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationUpdate, Table: "legacy", Journal: journalA,
	})
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if !errors.Is(executionErr, ErrSQLiteHoldAtomicWriteRecorded) || !errors.Is(updateErr, ErrSQLiteHoldAtomicWriteRecorded) {
		t.Fatalf("execution=%v update=%v", executionErr, updateErr)
	}
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if snapshot.Selected || snapshot.Operation != "" || snapshot.Journal != journalA {
		t.Fatalf("legacy write selected mismatched atomic transaction: %+v", snapshot)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); !errors.Is(err, ErrSQLiteHoldNotSelected) {
		t.Fatalf("expected mismatched atomic transaction to remain unselected, got %v", err)
	}
}

func TestSQLiteHoldTrackerRejectsAtomicWriteForUnregisteredTransaction(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(48)

	// When
	err := tracker.RecordSQLiteWrite(transaction, sqliteHoldTestWrite(SQLiteOperationInsert, "settings", 62, sqliteHoldTestJournal))

	// Then
	if !errors.Is(err, ErrSQLiteHoldNotSelected) {
		t.Fatalf("expected unregistered atomic write rejection, got %v", err)
	}
}
