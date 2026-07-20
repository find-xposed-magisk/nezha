//go:build agentcompat && linux

package singleton

import (
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type sqliteAttributionBlockingTx struct {
	commitStarted chan struct{}
	allowCommit   chan struct{}
	commitCalls   atomic.Int32
	rollbackCalls atomic.Int32
	lifecycleMu   *sync.Mutex
	lockFree      atomic.Bool
}

func (transaction *sqliteAttributionBlockingTx) Commit() error {
	transaction.commitCalls.Add(1)
	// Probe before publishing entry so losing terminal calls cannot contend with this boundary check.
	if transaction.lifecycleMu.TryLock() {
		transaction.lockFree.Store(true)
		transaction.lifecycleMu.Unlock()
	}
	close(transaction.commitStarted)
	<-transaction.allowCommit
	return nil
}

func (transaction *sqliteAttributionBlockingTx) Rollback() error {
	transaction.rollbackCalls.Add(1)
	return nil
}

func TestSQLiteAttributionTransactionCompletionRunsRawCommitExactlyOnce(t *testing.T) {
	// Given
	tracker := NewSQLiteHoldTracker()
	identity := sqliteHoldTestTransaction(201)
	if err := tracker.BeginSQLiteTransaction(identity); err != nil {
		t.Fatal(err)
	}
	raw := &sqliteAttributionBlockingTx{commitStarted: make(chan struct{}), allowCommit: make(chan struct{})}
	state := &sqliteAttributionTransaction{
		transaction: identity,
		raw:         raw,
		tracker:     tracker,
		journalFD:   -1,
		done:        make(chan struct{}),
	}
	var rawCloseCalls atomic.Int32
	connection := &sqliteAttributionConnection{transaction: state}
	connection.closeRawConnection = func() error {
		if !connection.lifecycleMu.TryLock() {
			return errors.New("raw connection Close ran while lifecycle lock was held")
		}
		connection.lifecycleMu.Unlock()
		rawCloseCalls.Add(1)
		return nil
	}
	raw.lifecycleMu = &connection.lifecycleMu
	owner := &sqliteAttributionTx{connection: connection, state: state}
	secondCommit := &sqliteAttributionTx{connection: connection, state: state}
	commitResult := make(chan error, 1)
	secondCommitResult := make(chan error, 1)
	rollbackResult := make(chan error, 1)
	closeResult := make(chan error, 1)
	secondCommitStarted := make(chan struct{})
	rollbackStarted := make(chan struct{})
	closeStarted := make(chan struct{})

	// When
	go func() { commitResult <- owner.Commit() }()
	<-raw.commitStarted
	go func() {
		close(secondCommitStarted)
		secondCommitResult <- secondCommit.Commit()
	}()
	go func() {
		close(rollbackStarted)
		rollbackResult <- owner.Rollback()
	}()
	go func() {
		close(closeStarted)
		closeResult <- connection.Close()
	}()
	<-secondCommitStarted
	<-rollbackStarted
	<-closeStarted
	for _, result := range []<-chan error{secondCommitResult, rollbackResult} {
		select {
		case err := <-result:
			t.Fatalf("completion loser returned before raw Commit completion: %v", err)
		default:
		}
	}
	select {
	case <-state.done:
		t.Fatal("completion published before raw Commit was allowed to finish")
	default:
	}
	if calls := rawCloseCalls.Load(); calls != 0 {
		t.Fatalf("raw connection Close calls while raw Commit was in flight = %d, want 0", calls)
	}
	if !raw.lockFree.Load() {
		t.Fatal("raw Commit ran while lifecycle lock was held")
	}
	if !sqliteAttributionTrackerTransactionActive(tracker, identity) {
		t.Fatal("tracker transaction became inactive while raw Commit was blocked")
	}
	close(raw.allowCommit)
	commitErr := <-commitResult
	secondCommitErr := <-secondCommitResult
	rollbackErr := <-rollbackResult
	closeErr := <-closeResult

	// Then
	if commitErr != nil {
		t.Fatalf("raw Commit error = %v", commitErr)
	}
	for _, loserErr := range []error{secondCommitErr, rollbackErr} {
		if !errors.Is(loserErr, driver.ErrBadConn) {
			t.Fatalf("completion loser error = %v, want driver.ErrBadConn", loserErr)
		}
	}
	if closeErr != nil && !errors.Is(closeErr, driver.ErrBadConn) {
		t.Fatalf("connection Close error = %v, want nil or driver.ErrBadConn", closeErr)
	}
	select {
	case <-state.done:
	default:
		t.Fatal("completion signal did not close after raw Commit finished")
	}
	if calls := raw.commitCalls.Load(); calls != 1 {
		t.Fatalf("raw Commit calls = %d, want 1", calls)
	}
	if calls := rawCloseCalls.Load(); calls != 1 {
		t.Fatalf("raw connection Close calls after raw Commit completed = %d, want 1", calls)
	}
	if calls := raw.rollbackCalls.Load(); calls != 0 {
		t.Fatalf("raw Rollback calls while raw Commit owned completion = %d, want 0", calls)
	}
	if _, _, active := sqliteAttributionTransactionState(t, connection); active {
		t.Fatal("completed transaction remained attached to the connection")
	}
	if sqliteAttributionTrackerTransactionActive(tracker, identity) {
		t.Fatal("completed transaction remained active in the tracker")
	}
}

var _ driver.Tx = (*sqliteAttributionBlockingTx)(nil)
