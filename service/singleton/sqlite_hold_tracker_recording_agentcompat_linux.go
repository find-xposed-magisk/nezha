//go:build agentcompat && linux

package singleton

func (tracker *SQLiteHoldTracker) BeginSQLiteTransaction(transaction SQLiteTransaction) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if _, active := tracker.transactions[transaction]; active {
		return ErrSQLiteHoldTransactionActive
	}
	tracker.transactions[transaction] = &sqliteHeldTransaction{transaction: transaction}
	return nil
}

func (tracker *SQLiteHoldTracker) RecordSQLiteExecution(transaction SQLiteTransaction, origin SQLiteExecutionOrigin) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	held, ok := tracker.transactionLocked(transaction)
	if !ok {
		return ErrSQLiteHoldNotSelected
	}
	if held.finalizing {
		return ErrSQLiteHoldFinalizationStarted
	}
	if held.hasWrite {
		return ErrSQLiteHoldAtomicWriteRecorded
	}
	if tracker.activeSessionHasSelectedTransactionLocked(transaction) {
		return ErrSQLiteHoldEvidenceFrozen
	}
	held.origin = origin
	return nil
}

func (tracker *SQLiteHoldTracker) RecordSQLiteUpdate(transaction SQLiteTransaction, update SQLiteUpdateObservation) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	held, ok := tracker.transactionLocked(transaction)
	if !ok {
		return ErrSQLiteHoldNotSelected
	}
	if held.finalizing {
		return ErrSQLiteHoldFinalizationStarted
	}
	if held.hasWrite {
		return ErrSQLiteHoldAtomicWriteRecorded
	}
	if tracker.activeSessionHasSelectedTransactionLocked(transaction) {
		return ErrSQLiteHoldEvidenceFrozen
	}
	held.update, held.hasUpdate = update, true
	if tracker.session != nil && !tracker.session.released && tracker.isEligibleLocked(held) {
		return tracker.selectSQLiteHoldLocked(transaction)
	}
	return nil
}

func (tracker *SQLiteHoldTracker) RecordSQLiteWrite(transaction SQLiteTransaction, write SQLiteWriteObservation) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	held, ok := tracker.transactionLocked(transaction)
	if !ok {
		return ErrSQLiteHoldNotSelected
	}
	if held.finalizing {
		return ErrSQLiteHoldFinalizationStarted
	}
	if held.hasWrite {
		return nil
	}
	if tracker.activeSessionHasSelectedTransactionLocked(transaction) {
		return ErrSQLiteHoldEvidenceFrozen
	}
	held.write, held.hasWrite = write, true
	held.origin, held.update, held.hasUpdate = write.Origin, write.Update, true
	if tracker.session != nil && !tracker.session.released && tracker.isEligibleLocked(held) {
		return tracker.selectSQLiteHoldLocked(transaction)
	}
	return nil
}

func (tracker *SQLiteHoldTracker) FinishSQLiteTransaction(transaction SQLiteTransaction) error {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if _, ok := tracker.transactionLocked(transaction); !ok {
		return ErrSQLiteHoldNotSelected
	}
	delete(tracker.transactions, transaction)
	defer delete(tracker.causes, transaction)
	defer delete(tracker.terminalArbitrations, transaction)
	if tracker.session != nil && tracker.session.hasSelected && tracker.session.selected == transaction {
		if tracker.session.released {
			tracker.terminal = &sqliteHoldTerminalState{id: tracker.session.id, selected: transaction, hasSelected: true, released: true}
			tracker.notifySQLiteHoldLocked()
			tracker.session = nil
		} else {
			tracker.abortSQLiteHoldLocked(ErrSQLiteHoldAborted)
		}
	}
	return nil
}

func (tracker *SQLiteHoldTracker) transactionLocked(transaction SQLiteTransaction) (*sqliteHeldTransaction, bool) {
	held, ok := tracker.transactions[transaction]
	return held, ok
}

func (tracker *SQLiteHoldTracker) isEligibleLocked(held *sqliteHeldTransaction) bool {
	if !held.hasUpdate || held.finalizing {
		return false
	}
	return tracker.session.mode == SQLiteHoldSelectionModeNextWriter || held.update.Journal == tracker.session.journal
}
