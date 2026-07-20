//go:build agentcompat && linux

package singleton

import (
	"database/sql/driver"
	"errors"
)

type SQLiteHoldError struct{ Cause error }

func (err *SQLiteHoldError) Error() string { return "sqlite hold finalization failed" }
func (err *SQLiteHoldError) Unwrap() error { return err.Cause }

type sqliteAttributionTx struct {
	connection *sqliteAttributionConnection
	state      *sqliteAttributionTransaction
}

var _ driver.Tx = (*sqliteAttributionTx)(nil)

func (transaction *sqliteAttributionTx) Commit() error {
	state := transaction.state
	transaction.connection.lifecycleMu.Lock()
	if state.terminalPhase != sqliteAttributionTerminalOpen {
		transaction.connection.lifecycleMu.Unlock()
		return state.waitForTerminal()
	}
	poison := state.poison
	if poison != nil {
		journalFD := state.claimTerminalLocked(sqliteAttributionTerminalRollbackOwned)
		transaction.connection.lifecycleMu.Unlock()
		if errors.Is(poison, ErrSQLiteHoldAmbiguousCandidate) {
			return errors.Join(&SQLiteHoldError{Cause: poison}, state.finish(transaction.connection, journalFD, state.raw.Rollback))
		}
		return errors.Join(poison, state.finish(transaction.connection, journalFD, state.raw.Rollback))
	}
	state.terminalPhase = sqliteAttributionTerminalCommitWaiting
	transaction.connection.lifecycleMu.Unlock()
	finalization, err := state.tracker.BeginSQLiteCommitFinalization(state.transaction)
	if errors.Is(err, ErrSQLiteHoldNotSelected) {
		return state.commit(transaction.connection)
	}
	if err != nil {
		return errors.Join(&SQLiteHoldError{Cause: err}, state.rollback(transaction.connection))
	}
	// Wait before raw Commit so the rollback journal stays observable for deterministic drain and prevents the baseline sample-1 FD regression.
	if err := state.tracker.WaitSQLiteCommitFinalization(state.context, finalization); err != nil {
		return errors.Join(&SQLiteHoldError{Cause: errors.Join(ErrSQLiteHoldAborted, err)}, state.rollback(transaction.connection))
	}
	if state.releasedCommitBoundary != nil {
		state.releasedCommitBoundary()
	}
	return state.commit(transaction.connection)
}

func (transaction *sqliteAttributionTx) Rollback() error {
	return transaction.state.rollback(transaction.connection)
}

func (state *sqliteAttributionTransaction) commit(connection *sqliteAttributionConnection) error {
	connection.lifecycleMu.Lock()
	if state.terminalPhase != sqliteAttributionTerminalCommitWaiting {
		connection.lifecycleMu.Unlock()
		return state.waitForTerminal()
	}
	journalFD := state.claimTerminalLocked(sqliteAttributionTerminalCommitOwned)
	connection.lifecycleMu.Unlock()
	return state.finish(connection, journalFD, state.raw.Commit)
}

func (state *sqliteAttributionTransaction) rollback(connection *sqliteAttributionConnection) error {
	connection.lifecycleMu.Lock()
	switch state.terminalPhase {
	case sqliteAttributionTerminalOpen:
		journalFD := state.claimTerminalLocked(sqliteAttributionTerminalRollbackOwned)
		connection.lifecycleMu.Unlock()
		return state.finish(connection, journalFD, state.raw.Rollback)
	case sqliteAttributionTerminalCommitWaiting:
		connection.lifecycleMu.Unlock()
		arbitration := state.tracker.ArbitrateSQLiteCommitWaitingTerminal(state.transaction)
		if arbitration == sqliteAttributionTerminalReleaseWon || arbitration == sqliteAttributionTerminalRollbackReserved {
			return state.waitForTerminal()
		}
		connection.lifecycleMu.Lock()
		if state.terminalPhase != sqliteAttributionTerminalCommitWaiting {
			connection.lifecycleMu.Unlock()
			return state.waitForTerminal()
		}
		journalFD := state.claimTerminalLocked(sqliteAttributionTerminalRollbackOwned)
		connection.lifecycleMu.Unlock()
		return state.finish(connection, journalFD, state.raw.Rollback)
	default:
		connection.lifecycleMu.Unlock()
		return state.waitForTerminal()
	}
}

func (state *sqliteAttributionTransaction) claimTerminalLocked(phase sqliteAttributionTerminalPhase) int {
	state.terminalPhase = phase
	journalFD := state.journalFD
	state.journalFD = -1
	return journalFD
}

func (state *sqliteAttributionTransaction) waitForTerminal() error {
	if state.terminalLoserWaitBoundary != nil {
		state.terminalLoserWaitBoundary()
	}
	<-state.done
	return driver.ErrBadConn
}

func (state *sqliteAttributionTransaction) finish(connection *sqliteAttributionConnection, journalFD int, completeRaw func() error) error {
	defer close(state.done)
	rawErr := completeRaw()
	connection.discardExecution()
	connection.lifecycleMu.Lock()
	if connection.transaction == state {
		connection.transaction = nil
	}
	connection.lifecycleMu.Unlock()
	closeErr := sqliteAttributionCloseJournalDescriptor(journalFD)
	trackerErr := state.tracker.FinishSQLiteTransaction(state.transaction)
	return errors.Join(rawErr, closeErr, trackerErr)
}
