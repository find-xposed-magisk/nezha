//go:build agentcompat

package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestAgentcompatCapabilityCancelAndUnregisterAreUniformAndNonMutating(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	handler := rpc.NewNezhaHandler()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = handler
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	ownerToken, ownerPAT := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	_, foreignPAT := mkDistinctCapabilityToken(t, userID, "foreign")
	server := newAgentcompatCapabilityServer(t)
	capability := registerAgentcompatCapability(t, server.URL, ownerPAT, agentcompatCapabilityRegisterRequest{Purpose: agentcompatCapabilityPurposeTerminal, ServerID: 7})
	bindAgentcompatTerminalCapability(t, handler, capability, ownerToken.ID, userID, 7, "private-stream")
	start := handler.SnapshotIOStreamState()
	unknown := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("u", 32)))

	cases := []struct {
		name string
		path string
		pat  string
		body string
	}{
		{name: "malformed cancel", path: agentcompatCapabilityCancelPath, pat: ownerPAT, body: `{"capability":`},
		{name: "oversize cancel", path: agentcompatCapabilityCancelPath, pat: ownerPAT, body: strings.Repeat("x", 513)},
		{name: "duplicate cancel", path: agentcompatCapabilityCancelPath, pat: ownerPAT, body: `{"capability":"x","capability":"y","purpose":"terminal","server_id":7}`},
		{name: "unknown cancel", path: agentcompatCapabilityCancelPath, pat: ownerPAT, body: capabilityAccessJSON(unknown, "terminal", 7, 0)},
		{name: "foreign cancel", path: agentcompatCapabilityCancelPath, pat: foreignPAT, body: capabilityAccessJSON(capability, "terminal", 7, 0)},
		{name: "purpose cancel", path: agentcompatCapabilityCancelPath, pat: ownerPAT, body: capabilityAccessJSON(capability, "file_manager", 7, 0)},
		{name: "server cancel", path: agentcompatCapabilityCancelPath, pat: ownerPAT, body: capabilityAccessJSON(capability, "terminal", 999999, 0)},
		{name: "invalid unregister", path: agentcompatCapabilityUnregisterPath, pat: ownerPAT, body: capabilityAccessJSON("invalid", "terminal", 7, 0)},
		{name: "oversize unregister", path: agentcompatCapabilityUnregisterPath, pat: ownerPAT, body: strings.Repeat("x", 513)},
		{name: "duplicate unregister", path: agentcompatCapabilityUnregisterPath, pat: ownerPAT, body: `{"capability":"x","purpose":"terminal","server_id":7,"server_id":8}`},
		{name: "foreign unregister", path: agentcompatCapabilityUnregisterPath, pat: foreignPAT, body: capabilityAccessJSON(capability, "terminal", 7, 0)},
		{name: "resource unregister", path: agentcompatCapabilityUnregisterPath, pat: ownerPAT, body: capabilityAccessJSON(capability, "terminal", 7, 41)},
	}
	var uniformBody string
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, body := postAgentcompatRaw(t, server.URL+testCase.path, testCase.pat, testCase.body)
			require.Equal(t, http.StatusOK, status)
			if uniformBody == "" {
				uniformBody = body
			}
			require.Equal(t, uniformBody, body)
			require.Equal(t, start, handler.SnapshotIOStreamState())
			require.NotContains(t, body, capability)
			require.NotContains(t, body, "private-stream")
		})
	}

	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	waited, err := postAgentcompatCapability[agentcompatCapabilityWaitRequest, agentcompatCapabilityWaitResponse](waitContext, server.URL+agentcompatCapabilityWaitPath, ownerPAT, agentcompatCapabilityWaitRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.NoError(t, err)
	require.Equal(t, "private-stream", waited.StreamID)
}

func TestAgentcompatCapabilityPermissionRevocationIsRecheckedWithoutMutation(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	handler := rpc.NewNezhaHandler()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = handler
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	token, plaintext := mkToken(t, userID, []string{model.ScopeServerRead}, []uint64{7})
	server := newAgentcompatCapabilityServer(t)
	capability := registerAgentcompatCapability(t, server.URL, plaintext, agentcompatCapabilityRegisterRequest{Purpose: "terminal", ServerID: 7})
	bindAgentcompatTerminalCapability(t, handler, capability, token.ID, userID, 7, "revoked-private-stream")
	start := handler.SnapshotIOStreamState()
	token.SetServerIDs([]uint64{99})
	require.NoError(t, singleton.DB.Model(token).Update("servers_csv", token.ServersCSV).Error)

	cancelStatus, cancelBody := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityCancelPath, plaintext, agentcompatCapabilityCancelRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	unregisterStatus, unregisterBody := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityUnregisterPath, plaintext, agentcompatCapabilityUnregisterRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.Equal(t, http.StatusOK, cancelStatus)
	require.Equal(t, http.StatusOK, unregisterStatus)
	require.Equal(t, cancelBody, unregisterBody)
	require.Equal(t, start, handler.SnapshotIOStreamState())

	token.SetServerIDs(nil)
	require.NoError(t, singleton.DB.Model(token).Update("servers_csv", "").Error)
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	waited, err := postAgentcompatCapability[agentcompatCapabilityWaitRequest, agentcompatCapabilityWaitResponse](waitContext, server.URL+agentcompatCapabilityWaitPath, plaintext, agentcompatCapabilityWaitRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.NoError(t, err)
	require.Equal(t, "revoked-private-stream", waited.StreamID)
}

func TestAgentcompatCapabilityForeignWaitAndRevokedPATCannotObserveBinding(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	handler := rpc.NewNezhaHandler()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = handler
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	ownerToken, ownerPAT := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	_, foreignPAT := mkDistinctCapabilityToken(t, userID, "foreign-wait")
	server := newAgentcompatCapabilityServer(t)
	capability := registerAgentcompatCapability(t, server.URL, ownerPAT, agentcompatCapabilityRegisterRequest{Purpose: "terminal", ServerID: 7})
	bindAgentcompatTerminalCapability(t, handler, capability, ownerToken.ID, userID, 7, "foreign-private-stream")
	start := handler.SnapshotIOStreamState()

	foreignContext, cancelForeign := context.WithTimeout(context.Background(), time.Second)
	defer cancelForeign()
	_, foreignErr := postAgentcompatCapability[agentcompatCapabilityWaitRequest, agentcompatCapabilityWaitResponse](foreignContext, server.URL+agentcompatCapabilityWaitPath, foreignPAT, agentcompatCapabilityWaitRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.Error(t, foreignErr)
	require.NotContains(t, foreignErr.Error(), capability)
	require.NotContains(t, foreignErr.Error(), "foreign-private-stream")
	require.Equal(t, start, handler.SnapshotIOStreamState())

	require.NoError(t, singleton.DB.Delete(&model.APIToken{}, ownerToken.ID).Error)
	status, body := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityWaitPath, ownerPAT, agentcompatCapabilityAccessRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.Equal(t, http.StatusUnauthorized, status)
	require.NotContains(t, body, capability)
	require.NotContains(t, body, "foreign-private-stream")
	require.Equal(t, start, handler.SnapshotIOStreamState())
}

func TestAgentcompatCapabilityRegisterRequiresCurrentServerWhitelist(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, plaintext := mkToken(t, userID, []string{model.ScopeServerRead}, []uint64{99})
	server := newAgentcompatCapabilityServer(t)

	status, body := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityRegisterPath, plaintext, agentcompatCapabilityRegisterRequest{Purpose: "file_manager", ServerID: 7})

	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, errAgentcompatCapabilityUnavailable.Error())
	require.NotContains(t, body, "99")
	require.NotContains(t, body, plaintext)
}

func TestAgentcompatCapabilityRegisterRejectsInvalidIdentityWithoutDisclosure(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, plaintext := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)
	privateServer := "777777777"
	cases := []string{
		`{`,
		`{"purpose":"unknown","server_id":7}`,
		`{"purpose":"terminal","server_id":0}`,
		`{"purpose":"terminal","server_id":7,"resource_id":41}`,
		`{"purpose":"nat","server_id":7}`,
		`{"purpose":"terminal","server_id":` + privateServer + `}`,
	}
	for _, body := range cases {
		status, responseBody := postAgentcompatRaw(t, server.URL+agentcompatCapabilityRegisterPath, plaintext, body)
		require.Equal(t, http.StatusOK, status)
		require.NotContains(t, responseBody, privateServer)
		require.NotContains(t, responseBody, plaintext)
		var envelope model.CommonResponse[agentcompatCapabilityRegisterResponse]
		require.NoError(t, json.Unmarshal([]byte(responseBody), &envelope))
		require.False(t, envelope.Success)
		require.Empty(t, envelope.Data.Capability)
	}
}

func newAgentcompatCapabilityServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func mkDistinctCapabilityToken(t *testing.T, userID uint64, suffix string) (*model.APIToken, string) {
	t.Helper()
	plaintext := "nzp_" + strings.Repeat("z", 32) + "_" + suffix
	token := &model.APIToken{UserID: userID, Name: suffix, TokenHash: model.HashAPIToken(plaintext)}
	token.SetScopes([]string{model.ScopeServerRead})
	require.NoError(t, singleton.DB.Create(token).Error)
	return token, plaintext
}

func bindAgentcompatTerminalCapability(t *testing.T, handler *rpc.NezhaHandler, rawCapability string, tokenID, userID, serverID uint64, streamID string) {
	t.Helper()
	capability, err := rpc.ParseAgentCompatIOStreamCapability(rawCapability)
	require.NoError(t, err)
	access := rpc.AgentCompatCapabilityAccess{
		Capability: capability, Owner: rpc.AgentCompatCapabilityOwner{PATID: tokenID, UserID: userID},
		Purpose: rpc.AgentCompatCapabilityTerminal, TargetServerID: serverID, ServerAccessAllowed: true,
	}
	require.NoError(t, handler.CreateStreamWithPurpose(streamID, userID, serverID, rpc.PurposeTerminal))
	require.NoError(t, handler.BindAgentCompatIOStreamCapability(rpc.AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: access, StreamID: streamID}))
}

func capabilityAccessJSON(capability, purpose string, serverID, resourceID uint64) string {
	body, _ := json.Marshal(agentcompatCapabilityAccessRequest{Capability: capability, Purpose: purpose, ServerID: serverID, ResourceID: resourceID})
	return string(body)
}

func postAgentcompatRaw(t *testing.T, path, token, body string) (int, string) {
	t.Helper()
	requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, path, bytes.NewBufferString(body))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(responseBody)
}
