//go:build agentcompat

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

type agentcompatTaskProbe struct {
	pb.NezhaService_RequestTaskServer
	mu       sync.Mutex
	test     *testing.T
	check    func(*testing.T)
	sendErr  error
	sendCall int
	task     *pb.Task
}

func (probe *agentcompatTaskProbe) Send(task *pb.Task) error {
	probe.mu.Lock()
	probe.sendCall++
	probe.task = task
	check := probe.check
	err := probe.sendErr
	probe.mu.Unlock()
	if check != nil {
		check(probe.test)
	}
	return err
}

func (probe *agentcompatTaskProbe) calls() int {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.sendCall
}

func (probe *agentcompatTaskProbe) Context() context.Context { return context.Background() }

func TestCreateTerminalAgentcompatBindsBeforeDispatchAndRemovesHeader(t *testing.T) {
	// Given
	handler, token, request := newAgentcompatCreateFixture(t, "POST", "/terminal", model.TerminalForm{ServerID: 7}, rpc.AgentCompatCapabilityTerminal)
	capability := registerAgentcompatForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal)
	request.Request.Header.Add(agentcompatcontract.IOStreamCapabilityHeader, capability)
	var waited string
	probe := &agentcompatTaskProbe{test: t}
	probe.check = func(t *testing.T) {
		access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal, capability)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		streamID, err := handler.WaitAgentCompatIOStreamCapability(ctx, access)
		require.NoError(t, err)
		waited = streamID
	}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(probe)

	// When
	response, err := createTerminal(request)

	// Then
	require.NoError(t, err)
	require.Equal(t, response.SessionID, waited)
	require.Empty(t, request.Request.Header.Values(agentcompatcontract.IOStreamCapabilityHeader))
	access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal, capability)
	_, waitErr := handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
	require.NoError(t, waitErr)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
	require.Equal(t, 0, handler.StreamCount())
}

func TestCreateFMAgentcompatRejectsTerminalCapabilityWithoutDispatch(t *testing.T) {
	// Given
	handler, token, request := newAgentcompatCreateFixture(t, "POST", "/file?id=7", nil, rpc.AgentCompatCapabilityFileManager)
	capability := registerAgentcompatForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal)
	request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	probe := &agentcompatTaskProbe{test: t}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(probe)

	// When
	response, err := createFM(request)

	// Then
	require.Error(t, err)
	require.Nil(t, response)
	require.Equal(t, 0, probe.sendCall)
	require.Equal(t, 0, handler.StreamCount())
}

func TestCreateTerminalAgentcompatRejectsFileManagerCapabilityWithoutDispatch(t *testing.T) {
	// Given
	handler, token, request := newAgentcompatCreateFixture(t, "POST", "/terminal", model.TerminalForm{ServerID: 7}, rpc.AgentCompatCapabilityTerminal)
	capability := registerAgentcompatForCreate(t, handler, token, rpc.AgentCompatCapabilityFileManager)
	request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	probe := &agentcompatTaskProbe{test: t}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(probe)

	// When
	response, err := createTerminal(request)

	// Then
	require.Error(t, err)
	require.Nil(t, response)
	require.Equal(t, 0, probe.calls())
	require.Equal(t, 0, handler.StreamCount())
	access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityFileManager, capability)
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
}

func TestCreateFMAgentcompatBindsBeforeDispatchWithExactTaskStreamID(t *testing.T) {
	// Given
	handler, token, request := newAgentcompatCreateFixture(t, "POST", "/file?id=7", nil, rpc.AgentCompatCapabilityFileManager)
	capability := registerAgentcompatForCreate(t, handler, token, rpc.AgentCompatCapabilityFileManager)
	request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	probe := &agentcompatTaskProbe{test: t}
	probe.check = func(t *testing.T) {
		access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityFileManager, capability)
		streamID, err := handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
		require.NoError(t, err)
		require.NotEmpty(t, streamID)
	}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(probe)

	// When
	response, err := createFM(request)

	// Then
	require.NoError(t, err)
	require.Equal(t, 1, probe.calls())
	require.NotNil(t, probe.task)
	require.Equal(t, uint64(model.TaskTypeFM), probe.task.Type)
	var task model.TaskFM
	require.NoError(t, json.Unmarshal([]byte(probe.task.Data), &task))
	require.Equal(t, response.SessionID, task.StreamID)
	require.Empty(t, request.Request.Header.Values(agentcompatcontract.IOStreamCapabilityHeader))
	access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityFileManager, capability)
	waited, err := handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
	require.NoError(t, err)
	require.Equal(t, response.SessionID, waited)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
}

func TestCreateTerminalAgentcompatSendFailureReleasesCapabilityAndStream(t *testing.T) {
	// Given
	handler, token, request := newAgentcompatCreateFixture(t, "POST", "/terminal", model.TerminalForm{ServerID: 7}, rpc.AgentCompatCapabilityTerminal)
	capability := registerAgentcompatForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal)
	request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(&agentcompatTaskProbe{test: t, sendErr: errors.New("dispatch failed")})

	// When
	response, err := createTerminal(request)

	// Then
	require.Error(t, err)
	require.Nil(t, response)
	require.Equal(t, 0, handler.StreamCount())
	access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal, capability)
	_, waitErr := handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
	require.Error(t, waitErr)
}

func newAgentcompatCreateFixture(t *testing.T, method, target string, body any, _ rpc.AgentCompatCapabilityPurpose) (*rpc.NezhaHandler, *model.APIToken, *gin.Context) {
	t.Helper()
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	handler := rpc.NewNezhaHandler()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = handler
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	token, _ := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	request := newAuthorizedControllerContext(t, method, target, body)
	request.Set(apiTokenCtxKey, token)
	request.Set(model.CtxKeyAPIToken, token)
	return handler, token, request
}

func registerAgentcompatForCreate(t *testing.T, handler *rpc.NezhaHandler, token *model.APIToken, purpose rpc.AgentCompatCapabilityPurpose) string {
	t.Helper()
	capability, err := handler.RegisterAgentCompatIOStreamCapability(context.Background(), rpc.AgentCompatCapabilityRegistration{
		Owner: rpc.AgentCompatCapabilityOwner{PATID: token.ID, UserID: token.UserID}, Purpose: purpose, TargetServerID: 7, ServerAccessAllowed: true,
	})
	require.NoError(t, err)
	return capability.String()
}

func agentcompatAccessForCreate(t *testing.T, handler *rpc.NezhaHandler, token *model.APIToken, purpose rpc.AgentCompatCapabilityPurpose, raw string) rpc.AgentCompatCapabilityAccess {
	t.Helper()
	capability, err := rpc.ParseAgentCompatIOStreamCapability(raw)
	require.NoError(t, err)
	return rpc.AgentCompatCapabilityAccess{Capability: capability, Owner: rpc.AgentCompatCapabilityOwner{PATID: token.ID, UserID: token.UserID}, Purpose: purpose, TargetServerID: 7, ServerAccessAllowed: true}
}
