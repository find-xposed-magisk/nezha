//go:build agentcompat && linux

package singleton

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
)

var ErrSQLiteHoldUnexpectedSelection = errors.New("sqlite hold selected an unexpected write")

type SQLiteHoldControlState string

const (
	SQLiteHoldControlStateArmed      SQLiteHoldControlState = "armed"
	SQLiteHoldControlStateSelected   SQLiteHoldControlState = "selected"
	SQLiteHoldControlStateFinalizing SQLiteHoldControlState = "finalizing"
	SQLiteHoldControlStateReleased   SQLiteHoldControlState = "released"
	SQLiteHoldControlStateAborted    SQLiteHoldControlState = "aborted"
)

type SQLiteHoldReceipt struct {
	ID    string                 `json:"id"`
	State SQLiteHoldControlState `json:"state"`
}

type sqliteHoldControlRecord struct {
	receipt SQLiteHoldReceipt
	session SQLiteHoldSession
}

type sqliteHoldControl struct {
	mu      sync.Mutex
	tracker *SQLiteHoldTracker
	random  io.Reader
	record  *sqliteHoldControlRecord
}

func newSQLiteHoldControl(tracker *SQLiteHoldTracker, randomSource io.Reader) *sqliteHoldControl {
	return &sqliteHoldControl{tracker: tracker, random: randomSource}
}

func newProductionSQLiteHoldControl(tracker *SQLiteHoldTracker) *sqliteHoldControl {
	return newSQLiteHoldControl(tracker, rand.Reader)
}

func (control *sqliteHoldControl) ArmNextSQLiteHold() (SQLiteHoldReceipt, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	identifier, err := control.newReceiptID()
	if err != nil {
		return SQLiteHoldReceipt{}, err
	}
	session, err := control.tracker.ArmNextSQLiteHold()
	if err != nil {
		return SQLiteHoldReceipt{}, err
	}
	receipt := SQLiteHoldReceipt{ID: identifier, State: SQLiteHoldControlStateArmed}
	control.record = &sqliteHoldControlRecord{receipt: receipt, session: session}
	return receipt, nil
}

func (control *sqliteHoldControl) WaitSelected(ctx context.Context, receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return control.wait(ctx, receipt, SQLiteHoldWaitSelected, SQLiteHoldControlStateSelected)
}

func (control *sqliteHoldControl) WaitFinalizing(ctx context.Context, receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return control.wait(ctx, receipt, SQLiteHoldWaitFinalizing, SQLiteHoldControlStateFinalizing)
}

func (control *sqliteHoldControl) wait(ctx context.Context, receipt SQLiteHoldReceipt, target SQLiteHoldWaitTarget, state SQLiteHoldControlState) (SQLiteHoldReceipt, error) {
	record, err := control.activeRecord(receipt)
	if err != nil {
		return SQLiteHoldReceipt{}, err
	}
	snapshot, err := control.tracker.WaitSQLiteHold(ctx, record.session, target)
	if err != nil {
		return control.terminalReceipt(record, err)
	}
	if snapshot.Operation != SQLiteOperationUpdate || snapshot.Table != "api_tokens" {
		_ = control.tracker.AbortSQLiteHold(record.session)
		return control.terminalReceipt(record, ErrSQLiteHoldUnexpectedSelection)
	}
	return control.updateReceipt(record, state), nil
}

func (control *sqliteHoldControl) Release(receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	record, err := control.activeRecord(receipt)
	if err != nil {
		return SQLiteHoldReceipt{}, err
	}
	if err := control.tracker.ReleaseSQLiteHold(record.session); err != nil {
		return control.terminalReceipt(record, err)
	}
	return control.updateReceipt(record, SQLiteHoldControlStateReleased), nil
}

func (control *sqliteHoldControl) Abort(receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	record, err := control.activeRecord(receipt)
	if err != nil {
		return SQLiteHoldReceipt{}, err
	}
	if err := control.tracker.AbortSQLiteHold(record.session); err != nil {
		return control.terminalReceipt(record, err)
	}
	return control.updateReceipt(record, SQLiteHoldControlStateAborted), nil
}

func (control *sqliteHoldControl) Snapshot(receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	record, err := control.activeRecord(receipt)
	if err != nil {
		return SQLiteHoldReceipt{}, err
	}
	current := control.currentReceipt(record)
	if current.State == SQLiteHoldControlStateReleased || current.State == SQLiteHoldControlStateAborted {
		return current, nil
	}
	snapshot, snapshotErr := control.tracker.SQLiteHoldSnapshot(record.session)
	if snapshotErr != nil {
		if errors.Is(snapshotErr, ErrSQLiteHoldStaleSession) {
			return control.updateReceipt(record, SQLiteHoldControlStateAborted), nil
		}
		return control.terminalReceipt(record, snapshotErr)
	}
	state := SQLiteHoldControlStateArmed
	if snapshot.Finalizing {
		state = SQLiteHoldControlStateFinalizing
	} else if snapshot.Selected {
		state = SQLiteHoldControlStateSelected
	}
	return control.updateReceipt(record, state), nil
}

func (control *sqliteHoldControl) currentReceipt(record *sqliteHoldControlRecord) SQLiteHoldReceipt {
	control.mu.Lock()
	defer control.mu.Unlock()
	return record.receipt
}

func (control *sqliteHoldControl) newReceiptID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := io.ReadFull(control.random, bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (control *sqliteHoldControl) activeRecord(receipt SQLiteHoldReceipt) (*sqliteHoldControlRecord, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.record == nil || control.record.receipt.ID != receipt.ID {
		return nil, ErrSQLiteHoldStaleSession
	}
	return control.record, nil
}

func (control *sqliteHoldControl) updateReceipt(record *sqliteHoldControlRecord, state SQLiteHoldControlState) SQLiteHoldReceipt {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.record == record {
		control.record.receipt.State = state
	}
	return record.receipt
}

func (control *sqliteHoldControl) terminalReceipt(record *sqliteHoldControlRecord, cause error) (SQLiteHoldReceipt, error) {
	state := SQLiteHoldControlStateAborted
	if cause == nil {
		state = SQLiteHoldControlStateReleased
	}
	return control.updateReceipt(record, state), cause
}
