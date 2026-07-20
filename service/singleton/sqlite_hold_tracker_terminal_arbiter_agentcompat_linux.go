//go:build agentcompat && linux

package singleton

type sqliteAttributionTerminalArbitration uint8

const (
	sqliteAttributionTerminalNoReservation sqliteAttributionTerminalArbitration = iota
	sqliteAttributionTerminalReleaseWon
	sqliteAttributionTerminalRollbackGranted
	sqliteAttributionTerminalRollbackReserved
)

func (tracker *SQLiteHoldTracker) ArbitrateSQLiteCommitWaitingTerminal(transaction SQLiteTransaction) sqliteAttributionTerminalArbitration {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.activeSessionHasSelectedTransactionLocked(transaction) {
		if _, reserved := tracker.terminalArbitrations[transaction]; reserved {
			return sqliteAttributionTerminalRollbackReserved
		}
		return sqliteAttributionTerminalNoReservation
	}
	if tracker.session.released {
		return sqliteAttributionTerminalReleaseWon
	}
	tracker.abortSQLiteHoldLocked(ErrSQLiteHoldAborted)
	tracker.terminalArbitrations[transaction] = struct{}{}
	return sqliteAttributionTerminalRollbackGranted
}
