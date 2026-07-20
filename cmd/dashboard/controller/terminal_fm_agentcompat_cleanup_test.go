//go:build agentcompat

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestCreateAgentcompatNoHeaderPreservesLegacyPurposeForTerminalAndFM(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		purpose rpc.AgentCompatCapabilityPurpose
		target  string
		body    any
	}{
		{name: "terminal", purpose: rpc.AgentCompatCapabilityTerminal, target: "/terminal", body: model.TerminalForm{ServerID: 7}},
		{name: "file manager", purpose: rpc.AgentCompatCapabilityFileManager, target: "/file?id=7"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler, token, request := newAgentcompatCreateFixture(t, "POST", testCase.target, testCase.body, testCase.purpose)
			capability := registerAgentcompatForCreate(t, handler, token, testCase.purpose)
			probe := &agentcompatTaskProbe{test: t}
			server, ok := singleton.ServerShared.Get(7)
			require.True(t, ok)
			server.SetTaskStream(probe)
			var responseStream string
			if testCase.purpose == rpc.AgentCompatCapabilityTerminal {
				response, err := createTerminal(request)
				require.NoError(t, err)
				responseStream = response.SessionID
			} else {
				response, err := createFM(request)
				require.NoError(t, err)
				responseStream = response.SessionID
			}
			require.Equal(t, 1, probe.calls())
			access := agentcompatAccessForCreate(t, handler, token, testCase.purpose, capability)
			require.ErrorIs(t, handler.BindAgentCompatIOStreamCapability(rpc.AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: access, StreamID: responseStream}), rpc.ErrAgentCompatCapabilityHidden)
			require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
			require.NoError(t, handler.CloseStream(responseStream))
		})
	}
}

func TestCreateAgentcompatSendFailureReleasesExactPATBoundaryForTerminalAndFM(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		purpose rpc.AgentCompatCapabilityPurpose
		target  string
		body    any
	}{
		{name: "terminal", purpose: rpc.AgentCompatCapabilityTerminal, target: "/terminal", body: model.TerminalForm{ServerID: 7}},
		{name: "file manager", purpose: rpc.AgentCompatCapabilityFileManager, target: "/file?id=7"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler, token, request := newAgentcompatCreateFixture(t, "POST", testCase.target, testCase.body, testCase.purpose)
			failed := registerAgentcompatForCreate(t, handler, token, testCase.purpose)
			request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, failed)
			server, ok := singleton.ServerShared.Get(7)
			require.True(t, ok)
			server.SetTaskStream(&agentcompatTaskProbe{test: t, sendErr: errors.New("dispatch failed")})
			if testCase.purpose == rpc.AgentCompatCapabilityTerminal {
				_, err := createTerminal(request)
				require.Error(t, err)
			} else {
				_, err := createFM(request)
				require.Error(t, err)
			}
			capabilities := make([]string, 0, 16)
			for range 16 {
				capabilities = append(capabilities, registerAgentcompatForCreate(t, handler, token, testCase.purpose))
			}
			_, err := handler.RegisterAgentCompatIOStreamCapability(context.Background(), rpc.AgentCompatCapabilityRegistration{Owner: rpc.AgentCompatCapabilityOwner{PATID: token.ID, UserID: token.UserID}, Purpose: testCase.purpose, TargetServerID: 7, ServerAccessAllowed: true})
			require.ErrorIs(t, err, rpc.ErrAgentCompatCapabilityUnavailable)
			for _, raw := range capabilities {
				access := agentcompatAccessForCreate(t, handler, token, testCase.purpose, raw)
				require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
			}
		})
	}
}

func TestCreateAgentcompatResponseLossWaitCancelForTerminalAndFM(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		purpose rpc.AgentCompatCapabilityPurpose
		target  string
		body    any
	}{
		{name: "terminal", purpose: rpc.AgentCompatCapabilityTerminal, target: "/terminal", body: model.TerminalForm{ServerID: 7}},
		{name: "file manager", purpose: rpc.AgentCompatCapabilityFileManager, target: "/file?id=7"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler, token, request := newAgentcompatCreateFixture(t, "POST", testCase.target, testCase.body, testCase.purpose)
			capability := registerAgentcompatForCreate(t, handler, token, testCase.purpose)
			request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
			probe := &agentcompatTaskProbe{test: t}
			server, ok := singleton.ServerShared.Get(7)
			require.True(t, ok)
			server.SetTaskStream(probe)
			var streamID string
			if testCase.purpose == rpc.AgentCompatCapabilityTerminal {
				response, err := createTerminal(request)
				require.NoError(t, err)
				streamID = response.SessionID
			} else {
				response, err := createFM(request)
				require.NoError(t, err)
				streamID = response.SessionID
			}
			access := agentcompatAccessForCreate(t, handler, token, testCase.purpose, capability)
			waited, err := handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
			require.NoError(t, err)
			require.Equal(t, streamID, waited)
			require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
			require.Equal(t, 0, handler.StreamCount())
			require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
			require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
		})
	}
}
