//go:build agentcompat && linux

package singleton

import "context"

// WaitSQLiteCommitFinalization linearizes cancellation against release under the tracker lock.
func (tracker *SQLiteHoldTracker) WaitSQLiteCommitFinalization(ctx context.Context, finalization *SQLiteHoldFinalization) error {
	for {
		tracker.mu.Lock()
		select {
		case <-finalization.done:
			err := finalization.err
			tracker.mu.Unlock()
			return err
		default:
		}
		if err := ctx.Err(); err != nil {
			if tracker.session != nil && tracker.session.finalization == finalization && !tracker.session.released {
				tracker.abortSQLiteHoldLocked(ErrSQLiteHoldAborted)
			}
			tracker.mu.Unlock()
			return err
		}
		done := finalization.done
		tracker.mu.Unlock()
		select {
		case <-done:
			return finalization.err
		case <-ctx.Done():
		}
	}
}
