//go:build agentcompat

package controller

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestCreateFMWrongRoutePreservesTerminalCapability(t *testing.T) {
	// Given
	handler, token, wrongRequest := newAgentcompatCreateFixture(t, "POST", "/file?id=7", nil, rpc.AgentCompatCapabilityFileManager)
	capability := registerAgentcompatForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal)
	wrongRequest.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	probe := &agentcompatTaskProbe{test: t}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(probe)

	// When
	response, err := createFM(wrongRequest)

	// Then
	require.Error(t, err)
	require.Nil(t, response)
	require.Empty(t, wrongRequest.Request.Header.Values(agentcompatcontract.IOStreamCapabilityHeader))
	require.Equal(t, 0, probe.calls())
	require.Equal(t, 0, handler.StreamCount())

	correctRequest := newAuthorizedControllerContext(t, "POST", "/terminal", model.TerminalForm{ServerID: 7})
	correctRequest.Set(apiTokenCtxKey, token)
	correctRequest.Set(model.CtxKeyAPIToken, token)
	correctRequest.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	terminalResponse, err := createTerminal(correctRequest)
	require.NoError(t, err)
	require.Equal(t, 1, probe.calls())
	var task model.TerminalTask
	require.NoError(t, json.Unmarshal([]byte(probe.task.Data), &task))
	require.Equal(t, terminalResponse.SessionID, task.StreamID)
	access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityTerminal, capability)
	waited, err := handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
	require.NoError(t, err)
	require.Equal(t, terminalResponse.SessionID, waited)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
	require.Equal(t, 0, handler.StreamCount())
}

func TestCreateTerminalWrongRoutePreservesFileManagerCapability(t *testing.T) {
	// Given
	handler, token, wrongRequest := newAgentcompatCreateFixture(t, "POST", "/terminal", model.TerminalForm{ServerID: 7}, rpc.AgentCompatCapabilityTerminal)
	capability := registerAgentcompatForCreate(t, handler, token, rpc.AgentCompatCapabilityFileManager)
	wrongRequest.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	probe := &agentcompatTaskProbe{test: t}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(probe)

	// When
	response, err := createTerminal(wrongRequest)

	// Then
	require.Error(t, err)
	require.Nil(t, response)
	require.Empty(t, wrongRequest.Request.Header.Values(agentcompatcontract.IOStreamCapabilityHeader))
	require.Equal(t, 0, probe.calls())
	require.Equal(t, 0, handler.StreamCount())

	correctRequest := newAuthorizedControllerContext(t, "POST", "/file?id=7", nil)
	correctRequest.Set(apiTokenCtxKey, token)
	correctRequest.Set(model.CtxKeyAPIToken, token)
	correctRequest.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
	fmResponse, err := createFM(correctRequest)
	require.NoError(t, err)
	require.Equal(t, 1, probe.calls())
	var task model.TaskFM
	require.NoError(t, json.Unmarshal([]byte(probe.task.Data), &task))
	require.Equal(t, fmResponse.SessionID, task.StreamID)
	access := agentcompatAccessForCreate(t, handler, token, rpc.AgentCompatCapabilityFileManager, capability)
	waited, err := handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
	require.NoError(t, err)
	require.Equal(t, fmResponse.SessionID, waited)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
	require.Equal(t, 0, handler.StreamCount())
}
