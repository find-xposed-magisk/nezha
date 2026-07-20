//go:build agentcompat && linux

package singleton

import (
	"errors"
	"testing"
)

func sqliteHoldTestTransactionOnConnection(connection SQLiteConnectionIdentity, id SQLiteTransactionIdentity) SQLiteTransaction {
	return SQLiteTransaction{Connection: connection, Identity: id}
}

func TestSQLiteHoldTrackerTracksSameTransactionIdentityAcrossConnections(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransactionOnConnection(3, 31)
	second := sqliteHoldTestTransactionOnConnection(4, 31)
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
		t.Fatalf("expected both connections to be tracked, got %v", err)
	}
}

func TestSQLiteHoldTrackerRejectsExactDuplicateTransaction(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransactionOnConnection(3, 32)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	err := tracker.BeginSQLiteTransaction(transaction)

	// Then
	if !errors.Is(err, ErrSQLiteHoldTransactionActive) {
		t.Fatalf("expected duplicate composite rejection, got %v", err)
	}
}

func TestSQLiteHoldTrackerFreezesSelectedMetadataAfterFinalization(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(33)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteExecution(transaction, SQLiteExecutionOrigin{
		Operation: SQLiteOperationInsert, Table: "initial", StackHash: 44, FirstNezhaFrame: "singleton.initial",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationInsert, Table: "initial", Journal: sqliteHoldTestJournal,
	}); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.BeginSQLiteFinalization(transaction); err != nil {
		t.Fatal(err)
	}

	// When
	executionErr := tracker.RecordSQLiteExecution(transaction, SQLiteExecutionOrigin{
		Operation: SQLiteOperationDelete, Table: "changed", StackHash: 45, FirstNezhaFrame: "singleton.changed",
	})
	updateErr := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationDelete, Table: "changed", Journal: SQLiteJournalIdentity{Inode: 99},
	})
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)

	// Then
	if !errors.Is(executionErr, ErrSQLiteHoldFinalizationStarted) || !errors.Is(updateErr, ErrSQLiteHoldFinalizationStarted) {
		t.Fatalf("execution=%v update=%v", executionErr, updateErr)
	}
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if snapshot.Operation != SQLiteOperationInsert || snapshot.Table != "initial" || snapshot.StackHash != 44 ||
		snapshot.FirstNezhaFrame != "singleton.initial" || snapshot.Journal != sqliteHoldTestJournal {
		t.Fatalf("finalizing metadata changed: %+v", snapshot)
	}
}

func TestSQLiteHoldTrackerRejectsSelectedReleaseBeforeFinalizationThenCompletes(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(34)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, transaction)
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}

	// When
	releaseErr := tracker.ReleaseSQLiteHold(session)
	finalization, finalizationErr := tracker.BeginSQLiteFinalization(transaction)

	// Then
	if !errors.Is(releaseErr, ErrSQLiteHoldFinalizationNotStarted) || finalizationErr != nil {
		t.Fatalf("release=%v finalization=%v", releaseErr, finalizationErr)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerCompletesSecondLifecycleAfterReleasedCleanup(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	first := sqliteHoldTestTransaction(35)
	if err := tracker.BeginSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, first)
	firstSession, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}
	firstFinalization, err := tracker.BeginSQLiteFinalization(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.ReleaseSQLiteHold(firstSession); err != nil {
		t.Fatal(err)
	}
	if err := firstFinalization.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishSQLiteTransaction(first); err != nil {
		t.Fatal(err)
	}
	second := sqliteHoldTestTransaction(36)
	if err := tracker.BeginSQLiteTransaction(second); err != nil {
		t.Fatal(err)
	}
	recordSQLiteHoldTestUpdate(t, tracker, second)

	// When
	secondSession, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	secondFinalization, finalizationErr := tracker.BeginSQLiteFinalization(second)

	// Then
	if err != nil || finalizationErr != nil {
		t.Fatalf("arm=%v finalization=%v", err, finalizationErr)
	}
	if err := tracker.ReleaseSQLiteHold(secondSession); err != nil {
		t.Fatal(err)
	}
	if err := secondFinalization.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := tracker.FinishSQLiteTransaction(second); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteHoldTrackerFreezesLegacyEvidenceWhenSelected(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	transaction := sqliteHoldTestTransaction(37)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	originalOrigin := SQLiteExecutionOrigin{
		Operation: SQLiteOperationInsert, Table: "original", StackHash: 46, FirstNezhaFrame: "singleton.original",
	}
	if err := tracker.RecordSQLiteExecution(transaction, originalOrigin); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationInsert, Table: "original", Journal: sqliteHoldTestJournal,
	}); err != nil {
		t.Fatal(err)
	}
	session, err := tracker.ArmSQLiteHold(sqliteHoldTestJournal)
	if err != nil {
		t.Fatal(err)
	}

	// When
	executionErr := tracker.RecordSQLiteExecution(transaction, SQLiteExecutionOrigin{
		Operation: SQLiteOperationDelete, Table: "changed", StackHash: 47, FirstNezhaFrame: "singleton.changed",
	})
	updateErr := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{
		Operation: SQLiteOperationDelete, Table: "changed", Journal: SQLiteJournalIdentity{Inode: 94},
	})
	snapshot, snapshotErr := tracker.SQLiteHoldSnapshot(session)
	finalization, finalizationErr := tracker.BeginSQLiteFinalization(transaction)

	// Then
	if !errors.Is(executionErr, ErrSQLiteHoldEvidenceFrozen) || !errors.Is(updateErr, ErrSQLiteHoldEvidenceFrozen) {
		t.Fatalf("execution=%v update=%v", executionErr, updateErr)
	}
	if snapshotErr != nil || finalizationErr != nil {
		t.Fatalf("snapshot=%v finalization=%v", snapshotErr, finalizationErr)
	}
	if snapshot.Operation != SQLiteOperationInsert || snapshot.Table != "original" || snapshot.StackHash != 46 ||
		snapshot.FirstNezhaFrame != "singleton.original" || snapshot.Journal != sqliteHoldTestJournal {
		t.Fatalf("selected legacy evidence changed: %+v", snapshot)
	}
	if err := tracker.ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	if err := finalization.Wait(); err != nil {
		t.Fatal(err)
	}
}
