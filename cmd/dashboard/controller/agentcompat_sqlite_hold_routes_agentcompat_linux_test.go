//go:build agentcompat && linux

package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

type agentcompatSQLiteHoldControlProbe struct {
	err              error
	armCalls         int
	lastCall         string
	waitContextError error
}

func (probe *agentcompatSQLiteHoldControlProbe) ArmNextSQLiteHold() (singleton.SQLiteHoldReceipt, error) {
	probe.armCalls++
	probe.lastCall = "arm"
	return agentcompatSQLiteHoldTestReceipt(singleton.SQLiteHoldControlStateArmed), probe.err
}

func (probe *agentcompatSQLiteHoldControlProbe) WaitSQLiteHoldSelected(ctx context.Context, receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	probe.lastCall = "wait-selected"
	probe.waitContextError = ctx.Err()
	receipt.State = singleton.SQLiteHoldControlStateSelected
	return receipt, probe.err
}

func (probe *agentcompatSQLiteHoldControlProbe) WaitSQLiteHoldFinalizing(ctx context.Context, receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	probe.lastCall = "wait-finalizing"
	probe.waitContextError = ctx.Err()
	receipt.State = singleton.SQLiteHoldControlStateFinalizing
	return receipt, probe.err
}

func (probe *agentcompatSQLiteHoldControlProbe) SnapshotSQLiteHold(receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	probe.lastCall = "snapshot"
	receipt.State = singleton.SQLiteHoldControlStateSelected
	return receipt, probe.err
}

func (probe *agentcompatSQLiteHoldControlProbe) ReleaseSQLiteHold(receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	probe.lastCall = "release"
	receipt.State = singleton.SQLiteHoldControlStateReleased
	return receipt, probe.err
}

func (probe *agentcompatSQLiteHoldControlProbe) AbortSQLiteHold(receipt singleton.SQLiteHoldReceipt) (singleton.SQLiteHoldReceipt, error) {
	probe.lastCall = "abort"
	receipt.State = singleton.SQLiteHoldControlStateAborted
	return receipt, probe.err
}

func TestAgentcompatSQLiteHoldRoutesRequirePATWithoutScopeOrAuthWrites(t *testing.T) {
	// Given
	router, token, storedToken, probe := setupAgentcompatSQLiteHoldRouteTest(t)

	// When
	status, response := requestAgentcompatSQLiteHold(t, router, context.Background(), "arm", token, `{}`)

	// Then
	require.Equal(t, http.StatusOK, status)
	require.True(t, response.Success)
	require.Equal(t, agentcompatSQLiteHoldTestReceipt(singleton.SQLiteHoldControlStateArmed), response.Data)
	require.Equal(t, 1, probe.armCalls)
	var refreshed model.APIToken
	require.NoError(t, singleton.DB.First(&refreshed, storedToken.ID).Error)
	require.Nil(t, refreshed.LastUsedAt)
	require.Empty(t, refreshed.LastUsedIP)

	for _, authorization := range []string{"", "jwt-looking-value", "nzp_invalid"} {
		status, _ = requestAgentcompatSQLiteHold(t, router, context.Background(), "arm", authorization, `{}`)
		require.Equal(t, http.StatusUnauthorized, status)
	}
	var wafRows int64
	require.NoError(t, singleton.DB.Model(&model.WAF{}).Count(&wafRows).Error)
	require.Zero(t, wafRows)
}

func TestAgentcompatSQLiteHoldRoutesRejectInvalidBodiesBeforeControl(t *testing.T) {
	// Given
	router, token, _, probe := setupAgentcompatSQLiteHoldRouteTest(t)
	tests := []string{"", `{`, `{"unknown":true}`, `{} {}`, strings.Repeat(" ", 513) + `{}`}

	for _, body := range tests {
		// When
		status, response := requestAgentcompatSQLiteHold(t, router, context.Background(), "arm", token, body)

		// Then
		require.Equal(t, http.StatusOK, status)
		require.False(t, response.Success)
		require.Equal(t, "agentcompat sqlite hold request is invalid", response.Error)
	}
	require.Zero(t, probe.armCalls)
}

func TestAgentcompatSQLiteHoldRoutesDispatchTypedLifecycleAndPropagateContext(t *testing.T) {
	// Given
	router, token, _, probe := setupAgentcompatSQLiteHoldRouteTest(t)
	receiptID := agentcompatSQLiteHoldTestReceipt("").ID
	tests := []struct {
		path      string
		body      string
		wantCall  string
		wantState singleton.SQLiteHoldControlState
	}{
		{"wait", `{"id":"` + receiptID + `","state":"selected"}`, "wait-selected", singleton.SQLiteHoldControlStateSelected},
		{"wait", `{"id":"` + receiptID + `","state":"finalizing"}`, "wait-finalizing", singleton.SQLiteHoldControlStateFinalizing},
		{"snapshot", `{"id":"` + receiptID + `"}`, "snapshot", singleton.SQLiteHoldControlStateSelected},
		{"release", `{"id":"` + receiptID + `"}`, "release", singleton.SQLiteHoldControlStateReleased},
		{"abort", `{"id":"` + receiptID + `"}`, "abort", singleton.SQLiteHoldControlStateAborted},
	}

	for _, test := range tests {
		// When
		status, response := requestAgentcompatSQLiteHold(t, router, context.Background(), test.path, token, test.body)

		// Then
		require.Equal(t, http.StatusOK, status)
		require.True(t, response.Success)
		require.Equal(t, test.wantCall, probe.lastCall)
		require.Equal(t, test.wantState, response.Data.State)
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	status, response := requestAgentcompatSQLiteHold(t, router, canceledContext, "wait", token, `{"id":"`+receiptID+`","state":"selected"}`)
	require.Equal(t, http.StatusOK, status)
	require.True(t, response.Success)
	require.ErrorIs(t, probe.waitContextError, context.Canceled)

	status, response = requestAgentcompatSQLiteHold(t, router, context.Background(), "wait", token, `{"id":"`+receiptID+`","state":"armed"}`)
	require.Equal(t, http.StatusOK, status)
	require.False(t, response.Success)
	require.Equal(t, "agentcompat sqlite hold request is invalid", response.Error)
}

func TestAgentcompatSQLiteHoldRoutesRedactControlErrors(t *testing.T) {
	// Given
	router, token, _, probe := setupAgentcompatSQLiteHoldRouteTest(t)
	probe.err = errors.New("token=nzp_secret path=/root/private dashboard.sqlite-journal")

	// When
	status, response := requestAgentcompatSQLiteHold(t, router, context.Background(), "arm", token, `{}`)

	// Then
	require.Equal(t, http.StatusOK, status)
	require.False(t, response.Success)
	require.Equal(t, "agentcompat sqlite hold control is unavailable", response.Error)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "nzp_secret")
	require.NotContains(t, string(encoded), "/root/private")
	require.NotContains(t, string(encoded), "journal")
}

func setupAgentcompatSQLiteHoldRouteTest(t *testing.T) (*gin.Engine, string, *model.APIToken, *agentcompatSQLiteHoldControlProbe) {
	t.Helper()
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	storedToken, token := mkToken(t, userID, nil, nil)
	probe := &agentcompatSQLiteHoldControlProbe{}
	originalControl := agentcompatSQLiteHoldController
	agentcompatSQLiteHoldController = probe
	t.Cleanup(func() { agentcompatSQLiteHoldController = originalControl })
	router := gin.New()
	registerAgentcompatSQLiteHoldRoutes(router, requiredAgentcompatPAT(apiTokenAuthMiddleware()))
	return router, token, storedToken, probe
}

func requestAgentcompatSQLiteHold(t *testing.T, router *gin.Engine, ctx context.Context, path, authorization, body string) (int, model.CommonResponse[singleton.SQLiteHoldReceipt]) {
	t.Helper()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, agentcompatSQLiteHoldPath+path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", "Bearer "+authorization)
	}
	responseRecorder := httptest.NewRecorder()
	router.ServeHTTP(responseRecorder, request)
	var response model.CommonResponse[singleton.SQLiteHoldReceipt]
	require.NoError(t, json.Unmarshal(responseRecorder.Body.Bytes(), &response))
	return responseRecorder.Code, response
}

func agentcompatSQLiteHoldTestReceipt(state singleton.SQLiteHoldControlState) singleton.SQLiteHoldReceipt {
	return singleton.SQLiteHoldReceipt{ID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32)), State: state}
}
