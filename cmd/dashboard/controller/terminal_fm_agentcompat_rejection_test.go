//go:build agentcompat

package controller

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
	"github.com/stretchr/testify/require"
)

func TestCreateIOStreamAgentcompatRejectsAdversarialHeadersSymmetrically(t *testing.T) {
	cases := []struct {
		name       string
		configure  func(*testing.T, *rpc.NezhaHandler, *model.APIToken, *gin.Context, string)
		purpose    rpc.AgentCompatCapabilityPurpose
		request    string
		wantHeader bool
	}{
		{name: "malformed terminal", purpose: rpc.AgentCompatCapabilityTerminal, request: "bad"},
		{name: "empty terminal", purpose: rpc.AgentCompatCapabilityTerminal, request: ""},
		{name: "duplicate terminal", purpose: rpc.AgentCompatCapabilityTerminal, request: "duplicate"},
		{name: "jwt only terminal", purpose: rpc.AgentCompatCapabilityTerminal, request: "jwt"},
		{name: "foreign pat terminal", purpose: rpc.AgentCompatCapabilityTerminal, request: "foreign"},
		{name: "whitelist terminal", purpose: rpc.AgentCompatCapabilityTerminal, request: "whitelist"},
		{name: "malformed file manager", purpose: rpc.AgentCompatCapabilityFileManager, request: "bad"},
		{name: "empty file manager", purpose: rpc.AgentCompatCapabilityFileManager, request: ""},
		{name: "duplicate file manager", purpose: rpc.AgentCompatCapabilityFileManager, request: "duplicate"},
		{name: "jwt only file manager", purpose: rpc.AgentCompatCapabilityFileManager, request: "jwt"},
		{name: "foreign pat file manager", purpose: rpc.AgentCompatCapabilityFileManager, request: "foreign"},
		{name: "whitelist file manager", purpose: rpc.AgentCompatCapabilityFileManager, request: "whitelist"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Given
			handler, token, request := newAgentcompatCreateFixture(t, "POST", createAgentcompatTarget(testCase.purpose), createAgentcompatBody(testCase.purpose), testCase.purpose)
			capability := registerAgentcompatForCreate(t, handler, token, testCase.purpose)
			request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, capability)
			if testCase.request == "duplicate" {
				request.Request.Header.Add(agentcompatcontract.IOStreamCapabilityHeader, capability)
			}
			if testCase.request == "bad" || testCase.request == "" {
				request.Request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, testCase.request)
			}
			if testCase.request == "jwt" {
				request.Set(apiTokenCtxKey, nil)
				request.Set(model.CtxKeyAPIToken, nil)
			}
			if testCase.request == "foreign" {
				foreign, _ := mkDistinctCapabilityToken(t, token.UserID, testCase.name)
				request.Set(apiTokenCtxKey, foreign)
				request.Set(model.CtxKeyAPIToken, foreign)
			}
			if testCase.request == "whitelist" {
				token.SetServerIDs([]uint64{99})
				require.NoError(t, singleton.DB.Model(token).Update("servers_csv", token.ServersCSV).Error)
			}
			if testCase.configure != nil {
				testCase.configure(t, handler, token, request, capability)
			}
			server, ok := singleton.ServerShared.Get(7)
			require.True(t, ok)
			probe := &agentcompatTaskProbe{test: t}
			server.SetTaskStream(probe)

			// When
			var err error
			if testCase.purpose == rpc.AgentCompatCapabilityTerminal {
				_, err = createTerminal(request)
			} else {
				_, err = createFM(request)
			}

			// Then
			require.Error(t, err)
			require.Equal(t, 0, probe.calls())
			require.Equal(t, 0, handler.StreamCount())
			require.Empty(t, request.Request.Header.Values(agentcompatcontract.IOStreamCapabilityHeader))
			ownerAccess := agentcompatAccessForCreate(t, handler, token, testCase.purpose, capability)
			require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(ownerAccess))
		})
	}
}

func createAgentcompatTarget(purpose rpc.AgentCompatCapabilityPurpose) string {
	if purpose == rpc.AgentCompatCapabilityTerminal {
		return "/terminal"
	}
	return "/file?id=7"
}

func createAgentcompatBody(purpose rpc.AgentCompatCapabilityPurpose) any {
	if purpose == rpc.AgentCompatCapabilityTerminal {
		return model.TerminalForm{ServerID: 7}
	}
	return nil
}
