//go:build agentcompat && linux

package singleton

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSQLiteHoldControlIssuesOpaqueReceiptAndStalesPriorReceipt(t *testing.T) {
	// Given
	control := newSQLiteHoldControl(NewSQLiteHoldTracker(), bytes.NewReader(append(bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)...)))

	// When
	first, firstErr := control.ArmNextSQLiteHold()
	if _, abortErr := control.Abort(first); abortErr != nil {
		t.Fatal(abortErr)
	}
	aborted, abortSnapshotErr := control.Snapshot(first)
	second, secondErr := control.ArmNextSQLiteHold()
	_, staleErr := control.Snapshot(first)

	// Then
	if firstErr != nil || secondErr != nil {
		t.Fatalf("first=%v second=%v", firstErr, secondErr)
	}
	if len(first.ID) != 43 || strings.Contains(first.ID, "=") || first.ID == second.ID {
		t.Fatalf("opaque receipt IDs = %q, %q", first.ID, second.ID)
	}
	if !errors.Is(staleErr, ErrSQLiteHoldStaleSession) {
		t.Fatalf("old receipt error = %v", staleErr)
	}
	if abortSnapshotErr != nil || aborted.State != SQLiteHoldControlStateAborted {
		t.Fatalf("aborted receipt=%+v err=%v", aborted, abortSnapshotErr)
	}
}

func TestSQLiteHoldControlReadsTerminalStatesAndRejectsUnexpectedSelection(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	control := newSQLiteHoldControl(tracker, bytes.NewReader(bytes.Repeat([]byte{9}, 64)))
	receipt, err := control.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	transaction := sqliteHoldTestTransaction(66)
	if err := tracker.BeginSQLiteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordSQLiteUpdate(transaction, SQLiteUpdateObservation{Operation: SQLiteOperationInsert, Table: "settings", Journal: sqliteHoldTestJournal}); err != nil {
		t.Fatal(err)
	}

	// When
	_, waitErr := control.WaitSelected(context.Background(), receipt)
	aborted, snapshotErr := control.Snapshot(receipt)
	wire, marshalErr := json.Marshal(aborted)

	// Then
	if !errors.Is(waitErr, ErrSQLiteHoldUnexpectedSelection) {
		t.Fatalf("unexpected selection error = %v", waitErr)
	}
	if snapshotErr != nil || aborted.State != SQLiteHoldControlStateAborted {
		t.Fatalf("terminal receipt=%+v err=%v", aborted, snapshotErr)
	}
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, forbidden := range []string{"transaction", "connection", "journal", "operation", "table", "path", "origin", "session_id"} {
		if strings.Contains(strings.ToLower(string(wire)), forbidden) {
			t.Fatalf("opaque receipt leaked %q: %s", forbidden, wire)
		}
	}
}

func TestSQLiteHoldControlReadsReleasedReceipt(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	control := newSQLiteHoldControl(tracker, bytes.NewReader(bytes.Repeat([]byte{11}, 32)))
	receipt, err := control.ArmNextSQLiteHold()
	if err != nil {
		t.Fatal(err)
	}
	transaction := sqliteHoldTestTransaction(67)
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
	_, releaseErr := control.Release(receipt)
	released, snapshotErr := control.Snapshot(receipt)
	_, abortErr := control.Abort(receipt)

	// Then
	if releaseErr != nil || snapshotErr != nil || released.State != SQLiteHoldControlStateReleased {
		t.Fatalf("release=%v receipt=%+v snapshot=%v", releaseErr, released, snapshotErr)
	}
	if !errors.Is(abortErr, ErrSQLiteHoldStaleSession) {
		t.Fatalf("abort after release = %v", abortErr)
	}
}
