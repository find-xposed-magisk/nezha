package client

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
)

var ErrInvalidSQLiteHoldReceipt = errors.New("client: invalid sqlite hold receipt")

type SQLiteHoldState string

const (
	SQLiteHoldStateArmed      SQLiteHoldState = "armed"
	SQLiteHoldStateSelected   SQLiteHoldState = "selected"
	SQLiteHoldStateFinalizing SQLiteHoldState = "finalizing"
	SQLiteHoldStateReleased   SQLiteHoldState = "released"
	SQLiteHoldStateAborted    SQLiteHoldState = "aborted"
)

type SQLiteHoldReceipt struct {
	ID    string          `json:"id"`
	State SQLiteHoldState `json:"state,omitempty"`
}

func (client *Client) ArmSQLiteHold(ctx context.Context) (SQLiteHoldReceipt, error) {
	receipt, err := DoREST[struct{}, SQLiteHoldReceipt](ctx, client, RESTRequest[struct{}]{Method: http.MethodPost, Path: "/agentcompat/sqlite-hold/arm", Body: &struct{}{}})
	return validateSQLiteHoldReceipt(receipt, SQLiteHoldStateArmed, err)
}

func (client *Client) WaitForSQLiteHold(ctx context.Context, receipt SQLiteHoldReceipt, target SQLiteHoldState) (SQLiteHoldReceipt, error) {
	if err := validateSQLiteHoldRequest(receipt, target); err != nil {
		return SQLiteHoldReceipt{}, err
	}
	request := SQLiteHoldReceipt{ID: receipt.ID, State: target}
	result, err := DoREST[SQLiteHoldReceipt, SQLiteHoldReceipt](ctx, client, RESTRequest[SQLiteHoldReceipt]{Method: http.MethodPost, Path: "/agentcompat/sqlite-hold/wait", Body: &request})
	return validateSQLiteHoldReceipt(result, target, err)
}

func (client *Client) SnapshotSQLiteHold(ctx context.Context, receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return client.sqliteHoldAction(ctx, receipt, "snapshot", "")
}

func (client *Client) ReleaseSQLiteHold(ctx context.Context, receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return client.sqliteHoldAction(ctx, receipt, "release", SQLiteHoldStateReleased)
}

func (client *Client) AbortSQLiteHold(ctx context.Context, receipt SQLiteHoldReceipt) (SQLiteHoldReceipt, error) {
	return client.sqliteHoldAction(ctx, receipt, "abort", SQLiteHoldStateAborted)
}

func (client *Client) sqliteHoldAction(ctx context.Context, receipt SQLiteHoldReceipt, action string, expected SQLiteHoldState) (SQLiteHoldReceipt, error) {
	if err := validateSQLiteHoldRequest(receipt, ""); err != nil {
		return SQLiteHoldReceipt{}, err
	}
	request := SQLiteHoldReceipt{ID: receipt.ID}
	result, err := DoREST[SQLiteHoldReceipt, SQLiteHoldReceipt](ctx, client, RESTRequest[SQLiteHoldReceipt]{Method: http.MethodPost, Path: "/agentcompat/sqlite-hold/" + action, Body: &request})
	return validateSQLiteHoldReceipt(result, expected, err)
}

func validateSQLiteHoldRequest(receipt SQLiteHoldReceipt, target SQLiteHoldState) error {
	decoded, err := base64.RawURLEncoding.DecodeString(receipt.ID)
	if err != nil || len(receipt.ID) != 43 || len(decoded) != 32 {
		return ErrInvalidSQLiteHoldReceipt
	}
	if target != "" && target != SQLiteHoldStateSelected && target != SQLiteHoldStateFinalizing {
		return ErrInvalidSQLiteHoldReceipt
	}
	return nil
}

func validateSQLiteHoldReceipt(receipt SQLiteHoldReceipt, expected SQLiteHoldState, err error) (SQLiteHoldReceipt, error) {
	if err != nil {
		return SQLiteHoldReceipt{}, err
	}
	if err := validateSQLiteHoldRequest(receipt, ""); err != nil || !validSQLiteHoldState(receipt.State) || expected != "" && receipt.State != expected {
		return SQLiteHoldReceipt{}, ErrInvalidSQLiteHoldReceipt
	}
	return receipt, nil
}

func validSQLiteHoldState(state SQLiteHoldState) bool {
	switch state {
	case SQLiteHoldStateArmed, SQLiteHoldStateSelected, SQLiteHoldStateFinalizing, SQLiteHoldStateReleased, SQLiteHoldStateAborted:
		return true
	default:
		return false
	}
}
