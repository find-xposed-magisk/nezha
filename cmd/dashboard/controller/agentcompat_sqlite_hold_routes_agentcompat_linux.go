//go:build agentcompat && linux

package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/service/singleton"
)

const agentcompatSQLiteHoldPath = "/agentcompat/sqlite-hold/"

type agentcompatSQLiteHoldControl interface {
	ArmNextSQLiteHold() (singleton.SQLiteHoldReceipt, error)
	WaitSQLiteHoldSelected(context.Context, singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error)
	WaitSQLiteHoldFinalizing(context.Context, singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error)
	SnapshotSQLiteHold(singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error)
	ReleaseSQLiteHold(singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error)
	AbortSQLiteHold(singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error)
}

type agentcompatSQLiteHoldFacade struct{}

func (agentcompatSQLiteHoldFacade) ArmNextSQLiteHold() (singleton.SQLiteHoldReceipt, error) {
	return singleton.ArmNextSQLiteHold()
}
func (agentcompatSQLiteHoldFacade) WaitSQLiteHoldSelected(ctx context.Context, receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	return singleton.WaitSQLiteHoldSelected(ctx, receipt)
}
func (agentcompatSQLiteHoldFacade) WaitSQLiteHoldFinalizing(ctx context.Context, receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	return singleton.WaitSQLiteHoldFinalizing(ctx, receipt)
}
func (agentcompatSQLiteHoldFacade) SnapshotSQLiteHold(receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	return singleton.SnapshotSQLiteHold(receipt)
}
func (agentcompatSQLiteHoldFacade) ReleaseSQLiteHold(receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	return singleton.ReleaseSQLiteHold(receipt)
}
func (agentcompatSQLiteHoldFacade) AbortSQLiteHold(receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	return singleton.AbortSQLiteHold(receipt)
}

var agentcompatSQLiteHoldController agentcompatSQLiteHoldControl = agentcompatSQLiteHoldFacade{}

type agentcompatSQLiteHoldRequest struct {
	ID    string                           `json:"id"`
	State singleton.SQLiteHoldControlState `json:"state"`
}

func registerAgentcompatSQLiteHoldRoutes(router *gin.Engine, patAuth gin.HandlerFunc) {
	readOnlyPAT := func(c *gin.Context) { suppressAPITokenAuthWrites(c) }
	router.POST(agentcompatSQLiteHoldPath+"arm", readOnlyPAT, patAuth, commonHandler(agentcompatSQLiteHoldArm))
	router.POST(agentcompatSQLiteHoldPath+"wait", readOnlyPAT, patAuth, commonHandler(agentcompatSQLiteHoldWait))
	router.POST(agentcompatSQLiteHoldPath+"snapshot", readOnlyPAT, patAuth, commonHandler(agentcompatSQLiteHoldSnapshot))
	router.POST(agentcompatSQLiteHoldPath+"release", readOnlyPAT, patAuth, commonHandler(agentcompatSQLiteHoldRelease))
	router.POST(agentcompatSQLiteHoldPath+"abort", readOnlyPAT, patAuth, commonHandler(agentcompatSQLiteHoldAbort))
}

func agentcompatSQLiteHoldArm(c *gin.Context) (singleton.SQLiteHoldReceipt, error) {
	var request struct{}
	if err := decodeAgentcompatSQLiteHoldRequest(c, &request); err != nil {
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldError(err)
	}
	receipt, err := agentcompatSQLiteHoldController.ArmNextSQLiteHold()
	return receipt, agentcompatSQLiteHoldError(err)
}
func agentcompatSQLiteHoldWait(c *gin.Context) (singleton.SQLiteHoldReceipt, error) {
	receipt, err := decodeAgentcompatSQLiteHoldReceipt(c, true)
	if err != nil {
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldError(err)
	}
	switch receipt.State {
	case singleton.SQLiteHoldControlStateSelected:
		receipt, err = agentcompatSQLiteHoldController.WaitSQLiteHoldSelected(c.Request.Context(), receipt)
	case singleton.SQLiteHoldControlStateFinalizing:
		receipt, err = agentcompatSQLiteHoldController.WaitSQLiteHoldFinalizing(c.Request.Context(), receipt)
	default:
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldInvalidRequest{}
	}
	return receipt, agentcompatSQLiteHoldError(err)
}
func agentcompatSQLiteHoldSnapshot(c *gin.Context) (singleton.SQLiteHoldReceipt, error) {
	receipt, err := decodeAgentcompatSQLiteHoldReceipt(c, false)
	if err != nil {
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldError(err)
	}
	result, err := agentcompatSQLiteHoldController.SnapshotSQLiteHold(receipt)
	return result, agentcompatSQLiteHoldError(err)
}
func agentcompatSQLiteHoldRelease(c *gin.Context) (singleton.SQLiteHoldReceipt, error) {
	receipt, err := decodeAgentcompatSQLiteHoldReceipt(c, false)
	if err != nil {
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldError(err)
	}
	result, err := agentcompatSQLiteHoldController.ReleaseSQLiteHold(receipt)
	return result, agentcompatSQLiteHoldError(err)
}
func agentcompatSQLiteHoldAbort(c *gin.Context) (singleton.SQLiteHoldReceipt, error) {
	receipt, err := decodeAgentcompatSQLiteHoldReceipt(c, false)
	if err != nil {
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldError(err)
	}
	result, err := agentcompatSQLiteHoldController.AbortSQLiteHold(receipt)
	return result, agentcompatSQLiteHoldError(err)
}

type agentcompatSQLiteHoldInvalidRequest struct{}

func (agentcompatSQLiteHoldInvalidRequest) Error() string {
	return "agentcompat sqlite hold request is invalid"
}

func decodeAgentcompatSQLiteHoldRequest(c *gin.Context, value any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return agentcompatSQLiteHoldInvalidRequest{}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return agentcompatSQLiteHoldInvalidRequest{}
	}
	return nil
}
func decodeAgentcompatSQLiteHoldReceipt(c *gin.Context, requireState bool) (singleton.SQLiteHoldReceipt, error) {
	var request agentcompatSQLiteHoldRequest
	if err := decodeAgentcompatSQLiteHoldRequest(c, &request); err != nil {
		return singleton.SQLiteHoldReceipt{}, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(request.ID)
	if err != nil || len(request.ID) != 43 || len(decoded) != 32 {
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldInvalidRequest{}
	}
	if !requireState && request.State != "" {
		return singleton.SQLiteHoldReceipt{}, agentcompatSQLiteHoldInvalidRequest{}
	}
	return singleton.SQLiteHoldReceipt{ID: request.ID, State: request.State}, nil
}
func agentcompatSQLiteHoldError(err error) error {
	if err == nil {
		return nil
	}
	if errors.As(err, new(agentcompatSQLiteHoldInvalidRequest)) {
		return agentcompatSQLiteHoldInvalidRequest{}
	}
	switch {
	case errors.Is(err, singleton.ErrSQLiteHoldSessionActive):
		return errors.New("agentcompat sqlite hold is active")
	case errors.Is(err, singleton.ErrSQLiteHoldStaleSession):
		return errors.New("agentcompat sqlite hold receipt is stale")
	case errors.Is(err, singleton.ErrSQLiteHoldFinalizationNotStarted), errors.Is(err, singleton.ErrSQLiteHoldFinalizationStarted):
		return errors.New("agentcompat sqlite hold is not ready")
	case errors.Is(err, singleton.ErrSQLiteHoldUnexpectedSelection), errors.Is(err, singleton.ErrSQLiteHoldAmbiguousCandidate), errors.Is(err, singleton.ErrSQLiteHoldAborted):
		return errors.New("agentcompat sqlite hold was aborted")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.New("agentcompat sqlite hold wait was canceled")
	default:
		return errors.New("agentcompat sqlite hold control is unavailable")
	}
}
