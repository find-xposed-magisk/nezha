//go:build agentcompat && linux

package singleton

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSQLiteAttributionCommitReleaseLinearizesBeforeContextCancellation(t *testing.T) {
	// Given
	transactionContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, transaction, session, databasePath := sqliteAttributionHeldTransaction(t, "released-before-cancel", transactionContext)
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("released transaction is not active")
	}

	// When
	commit := sqliteAttributionStartHeldCommit(t, transaction, session)
	if err := sqliteAttributionTracker.Load().ReleaseSQLiteHold(session); err != nil {
		t.Fatal(err)
	}
	cancel()
	commitErr := <-commit
	terminal, terminalErr := sqliteAttributionTracker.Load().WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)

	// Then
	if commitErr != nil {
		t.Fatalf("released Commit error after context cancellation = %v", commitErr)
	}
	if terminalErr != nil || !terminal.Released || !terminal.Selected || !terminal.Finalizing {
		t.Fatalf("released terminal=%+v err=%v", terminal, terminalErr)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after released Commit = %v, want EBADF", descriptorErr)
	}
	if count := sqliteAttributionPersistedCount(t, databasePath); count != 1 {
		t.Fatalf("released Commit persisted %d rows", count)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Fatal("released Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Fatal("released Commit left the tracker transaction active")
	}
}

func TestSQLiteAttributionCommitCancellationAbortsBeforeRelease(t *testing.T) {
	// Given
	transactionContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection, transaction, session, databasePath := sqliteAttributionHeldTransaction(t, "cancelled-before-release", transactionContext)
	identity, descriptor, active := sqliteAttributionTransactionState(t, connection)
	if !active {
		t.Fatal("cancelled transaction is not active")
	}

	// When
	commit := sqliteAttributionStartHeldCommit(t, transaction, session)
	cancel()
	commitErr := <-commit
	releaseErr := sqliteAttributionTracker.Load().ReleaseSQLiteHold(session)
	_, terminalErr := sqliteAttributionTracker.Load().WaitSQLiteHold(context.Background(), session, SQLiteHoldWaitFinalizing)
	_, descriptorErr := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
	tracker := sqliteAttributionTracker.Load()
	tracker.mu.Lock()
	terminal := tracker.terminal
	tracker.mu.Unlock()

	// Then
	var holdErr *SQLiteHoldError
	if !errors.As(commitErr, &holdErr) || !errors.Is(commitErr, ErrSQLiteHoldAborted) || !errors.Is(commitErr, context.Canceled) {
		t.Fatalf("cancelled Commit error = %v", commitErr)
	}
	if !errors.Is(releaseErr, ErrSQLiteHoldStaleSession) {
		t.Fatalf("release after cancellation-owned abort = %v", releaseErr)
	}
	if !errors.Is(terminalErr, ErrSQLiteHoldAborted) {
		t.Fatalf("cancelled terminal wait error = %v, want aborted", terminalErr)
	}
	if terminal == nil || terminal.released {
		t.Fatalf("cancelled terminal state = %+v, want aborted and unreleased", terminal)
	}
	if !errors.Is(descriptorErr, unix.EBADF) {
		t.Fatalf("journal descriptor after cancelled Commit = %v, want EBADF", descriptorErr)
	}
	if count := sqliteAttributionPersistedCount(t, databasePath); count != 0 {
		t.Fatalf("cancelled Commit persisted %d rows", count)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Fatal("cancelled Commit left the connection transaction active")
	}
	if sqliteAttributionTrackerTransactionActive(sqliteAttributionTracker.Load(), identity) {
		t.Fatal("cancelled Commit left the tracker transaction active")
	}
}
