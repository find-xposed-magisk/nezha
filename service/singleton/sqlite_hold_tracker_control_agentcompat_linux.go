//go:build agentcompat && linux

package singleton

import "context"

func (tracker *SQLiteHoldTracker) ArmSQLiteHold(journal SQLiteJournalIdentity) (SQLiteHoldSession, error) {
	return tracker.armSQLiteHold(SQLiteHoldSelectionModeKnownJournal, journal)
}

func (tracker *SQLiteHoldTracker) ArmNextSQLiteHold() (SQLiteHoldSession, error) {
	return tracker.armSQLiteHold(SQLiteHoldSelectionModeNextWriter, SQLiteJournalIdentity{})
}

func (tracker *SQLiteHoldTracker) armSQLiteHold(mode SQLiteHoldSelectionMode, journal SQLiteJournalIdentity) (SQLiteHoldSession, error) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.session != nil {
		return SQLiteHoldSession{}, ErrSQLiteHoldSessionActive
	}
	tracker.nextSession++
	tracker.session = &sqliteHoldSessionState{
		id: SQLiteHoldSession{identity: tracker.nextSession}, mode: mode, journal: journal, notify: make(chan struct{}),
	}
	var candidates []SQLiteTransaction
	for transaction, held := range tracker.transactions {
		if tracker.isEligibleLocked(held) {
			candidates = append(candidates, transaction)
		}
	}
	if len(candidates) > 1 {
		tracker.abortSQLiteHoldWithCandidatesLocked(ErrSQLiteHoldAmbiguousCandidate, candidates)
		return SQLiteHoldSession{}, ErrSQLiteHoldAmbiguousCandidate
	}
	if len(candidates) == 1 {
		tracker.selectSQLiteHoldLocked(candidates[0])
	}
	tracker.setAttributionEnabledLocked(true)
	return tracker.session.id, nil
}

func (tracker *SQLiteHoldTracker) WaitSQLiteHold(ctx context.Context, session SQLiteHoldSession, target SQLiteHoldWaitTarget) (SQLiteHoldSnapshot, error) {
	if !target.valid() {
		return SQLiteHoldSnapshot{}, ErrSQLiteHoldInvalidWaitTarget
	}
	for {
		tracker.mu.Lock()
		if tracker.session == nil || tracker.session.id != session {
			if tracker.terminal != nil && tracker.terminal.id == session {
				if tracker.terminal.released {
					snapshot := SQLiteHoldSnapshot{SessionID: session.ID(), Selected: tracker.terminal.hasSelected, Transaction: tracker.terminal.selected, Finalizing: true, Released: true}
					tracker.mu.Unlock()
					return snapshot, nil
				}
				cause := tracker.terminal.cause
				tracker.mu.Unlock()
				return SQLiteHoldSnapshot{}, cause
			}
			tracker.mu.Unlock()
			return SQLiteHoldSnapshot{}, ErrSQLiteHoldStaleSession
		}
		if tracker.waitTargetReachedLocked(target) {
			snapshot, err := tracker.snapshotLocked(session)
			tracker.mu.Unlock()
			return snapshot, err
		}
		notify := tracker.session.notify
		tracker.mu.Unlock()
		select {
		case <-notify:
		case <-ctx.Done():
			return SQLiteHoldSnapshot{}, ctx.Err()
		}
	}
}

func (tracker *SQLiteHoldTracker) BeginSQLiteFinalization(transaction SQLiteTransaction) (*SQLiteHoldFinalization, error) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.session == nil || !tracker.session.hasSelected || tracker.session.selected != transaction {
		return nil, ErrSQLiteHoldNotSelected
	}
	return tracker.beginSQLiteFinalizationLocked(transaction)
}

func (tracker *SQLiteHoldTracker) BeginSQLiteCommitFinalization(transaction SQLiteTransaction) (*SQLiteHoldFinalization, error) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.session != nil && tracker.session.hasSelected && tracker.session.selected == transaction {
		return tracker.beginSQLiteFinalizationLocked(transaction)
	}
	if cause, ok := tracker.causes[transaction]; ok {
		return nil, cause
	}
	return nil, ErrSQLiteHoldNotSelected
}

func (tracker *SQLiteHoldTracker) ReleaseSQLiteHold(session SQLiteHoldSession) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.matchesSessionLocked(session) || tracker.session.released {
		return ErrSQLiteHoldStaleSession
	}
	if !tracker.session.hasSelected || tracker.session.finalization == nil {
		return ErrSQLiteHoldFinalizationNotStarted
	}
	tracker.session.released = true
	tracker.setAttributionEnabledLocked(false)
	close(tracker.session.finalization.done)
	tracker.notifySQLiteHoldLocked()
	return nil
}

func (tracker *SQLiteHoldTracker) AbortSQLiteHold(session SQLiteHoldSession) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.matchesSessionLocked(session) || tracker.session.released {
		return ErrSQLiteHoldStaleSession
	}
	tracker.abortSQLiteHoldLocked(ErrSQLiteHoldAborted)
	return nil
}

func (tracker *SQLiteHoldTracker) SQLiteHoldSnapshot(session SQLiteHoldSession) (SQLiteHoldSnapshot, error) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.matchesSessionLocked(session) {
		return SQLiteHoldSnapshot{}, ErrSQLiteHoldStaleSession
	}
	return tracker.snapshotLocked(session)
}

func (tracker *SQLiteHoldTracker) snapshotLocked(session SQLiteHoldSession) (SQLiteHoldSnapshot, error) {
	snapshot := SQLiteHoldSnapshot{SessionID: session.ID(), Mode: tracker.session.mode, Selected: tracker.session.hasSelected, Journal: tracker.session.journal, Released: tracker.session.released}
	if !tracker.session.hasSelected {
		return snapshot, nil
	}
	held := tracker.transactions[tracker.session.selected]
	if held == nil {
		return SQLiteHoldSnapshot{}, ErrSQLiteHoldNotSelected
	}
	snapshot.Transaction = held.transaction
	if held.hasWrite {
		snapshot.Operation, snapshot.Table, snapshot.Journal = held.write.Update.Operation, held.write.Update.Table, held.write.Update.Journal
		snapshot.StackHash, snapshot.FirstNezhaFrame = held.write.Origin.StackHash, held.write.Origin.FirstNezhaFrame
	} else {
		snapshot.Operation, snapshot.Table, snapshot.Journal = held.update.Operation, held.update.Table, held.update.Journal
		snapshot.StackHash, snapshot.FirstNezhaFrame = held.origin.StackHash, held.origin.FirstNezhaFrame
	}
	snapshot.Finalizing = held.finalizing
	return snapshot, nil
}

func (tracker *SQLiteHoldTracker) selectSQLiteHoldLocked(transaction SQLiteTransaction) error {
	if !tracker.session.hasSelected {
		held := tracker.transactions[transaction]
		tracker.session.selected, tracker.session.hasSelected = transaction, true
		if tracker.session.mode == SQLiteHoldSelectionModeNextWriter {
			tracker.session.journal = held.update.Journal
		}
		tracker.notifySQLiteHoldLocked()
		return nil
	}
	if tracker.session.selected == transaction {
		return nil
	}
	tracker.abortSQLiteHoldWithCandidatesLocked(ErrSQLiteHoldAmbiguousCandidate, []SQLiteTransaction{tracker.session.selected, transaction})
	return ErrSQLiteHoldAmbiguousCandidate
}

func (tracker *SQLiteHoldTracker) matchesSessionLocked(session SQLiteHoldSession) bool {
	return tracker.session != nil && tracker.session.id == session
}

func (tracker *SQLiteHoldTracker) activeSessionHasSelectedTransactionLocked(transaction SQLiteTransaction) bool {
	return tracker.session != nil && tracker.session.hasSelected && tracker.session.selected == transaction
}

func (tracker *SQLiteHoldTracker) beginSQLiteFinalizationLocked(transaction SQLiteTransaction) (*SQLiteHoldFinalization, error) {
	held, ok := tracker.transactionLocked(transaction)
	if !ok || held.finalizing {
		return nil, ErrSQLiteHoldFinalizationStarted
	}
	held.finalizing = true
	finalization := &SQLiteHoldFinalization{done: make(chan struct{})}
	tracker.session.finalization = finalization
	tracker.notifySQLiteHoldLocked()
	return finalization, nil
}

func (tracker *SQLiteHoldTracker) waitTargetReachedLocked(target SQLiteHoldWaitTarget) bool {
	switch target {
	case SQLiteHoldWaitSelected:
		return tracker.session.hasSelected
	case SQLiteHoldWaitFinalizing:
		return tracker.session.finalization != nil
	default:
		return false
	}
}

func (target SQLiteHoldWaitTarget) valid() bool {
	return target == SQLiteHoldWaitSelected || target == SQLiteHoldWaitFinalizing
}

// Closing and replacing the channel under mu makes a snapshot-to-wait handoff race-free.
func (tracker *SQLiteHoldTracker) notifySQLiteHoldLocked() {
	close(tracker.session.notify)
	tracker.session.notify = make(chan struct{})
}

func (tracker *SQLiteHoldTracker) abortSQLiteHoldLocked(cause error) {
	tracker.abortSQLiteHoldWithCandidatesLocked(cause, nil)
}

func (tracker *SQLiteHoldTracker) abortSQLiteHoldWithCandidatesLocked(cause error, candidates []SQLiteTransaction) {
	tracker.setAttributionEnabledLocked(false)
	if tracker.session.finalization != nil {
		tracker.session.finalization.err = cause
		close(tracker.session.finalization.done)
	}
	if tracker.session.hasSelected {
		tracker.causes[tracker.session.selected] = cause
	}
	for _, transaction := range candidates {
		tracker.causes[transaction] = cause
	}
	tracker.terminal = &sqliteHoldTerminalState{id: tracker.session.id, selected: tracker.session.selected, hasSelected: tracker.session.hasSelected, cause: cause}
	tracker.notifySQLiteHoldLocked()
	tracker.session = nil
}

func (tracker *SQLiteHoldTracker) setAttributionEnabledLocked(enabled bool) {
	if tracker.attributionEnabled == nil {
		return
	}
	// Attribution is hold-scoped because ordinary GORM RETURNING closes rows before EOF.
	tracker.attributionEnabled.Store(enabled)
}
