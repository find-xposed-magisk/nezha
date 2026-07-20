//go:build agentcompat

package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
)

type agentcompatCapabilityCloseFailure struct {
	closed atomic.Int32
}

func (*agentcompatCapabilityCloseFailure) Read([]byte) (int, error)       { return 0, io.EOF }
func (*agentcompatCapabilityCloseFailure) Write(data []byte) (int, error) { return len(data), nil }
func (failure *agentcompatCapabilityCloseFailure) Close() error {
	failure.closed.Add(1)
	return errors.New("private-stream private-capability private-server")
}

func TestAgentcompatCapabilityBoundUnregisterAndCancelCleanupErrorsAreGeneric(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	handler := rpc.NewNezhaHandler()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = handler
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	token, plaintext := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)
	capability := registerAgentcompatCapability(t, server.URL, plaintext, agentcompatCapabilityRegisterRequest{Purpose: "file_manager", ServerID: 7})
	parsed, err := rpc.ParseAgentCompatIOStreamCapability(capability)
	require.NoError(t, err)
	access := rpc.AgentCompatCapabilityAccess{
		Capability: parsed, Owner: rpc.AgentCompatCapabilityOwner{PATID: token.ID, UserID: userID},
		Purpose: rpc.AgentCompatCapabilityFileManager, TargetServerID: 7, ServerAccessAllowed: true,
	}
	require.NoError(t, handler.CreateStreamWithPurpose("private-cleanup-stream", userID, 7, rpc.PurposeFileManager))
	require.NoError(t, handler.BindAgentCompatIOStreamCapability(rpc.AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: access, StreamID: "private-cleanup-stream"}))

	unregisterStatus, unregisterBody := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityUnregisterPath, plaintext, agentcompatCapabilityUnregisterRequest{Capability: capability, Purpose: "file_manager", ServerID: 7})
	require.Equal(t, http.StatusOK, unregisterStatus)
	require.Contains(t, unregisterBody, errAgentcompatCapabilityConflict.Error())
	require.NotContains(t, unregisterBody, capability)
	require.NotContains(t, unregisterBody, "private-cleanup-stream")

	failure := &agentcompatCapabilityCloseFailure{}
	require.NoError(t, handler.UserConnected("private-cleanup-stream", failure))
	cancelStatus, cancelBody := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityCancelPath, plaintext, agentcompatCapabilityCancelRequest{Capability: capability, Purpose: "file_manager", ServerID: 7})
	require.Equal(t, http.StatusOK, cancelStatus)
	require.Contains(t, cancelBody, errAgentcompatCapabilityCleanup.Error())
	require.NotContains(t, cancelBody, capability)
	require.NotContains(t, cancelBody, "private-cleanup-stream")
	require.Equal(t, int32(1), failure.closed.Load())
}

func TestAgentcompatCapabilityWaitHonorsRequestCancellation(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, plaintext := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)
	capability := registerAgentcompatCapability(t, server.URL, plaintext, agentcompatCapabilityRegisterRequest{Purpose: "terminal", ServerID: 7})
	requestContext, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	body := strings.NewReader(capabilityAccessJSON(capability, "terminal", 7, 0))
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, server.URL+agentcompatCapabilityWaitPath, body)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+plaintext)
	request.Header.Set("Content-Type", "application/json")
	_, err = server.Client().Do(request)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}
