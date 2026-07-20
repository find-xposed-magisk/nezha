//go:build agentcompat

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

func TestAgentcompatCapabilityRoutesRequirePATAndDoNotAcceptOwnerFields(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	router := gin.New()
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	requestBody := `{"purpose":"terminal","server_id":7,"pat_id":999,"user_id":999,"is_admin":true}`
	requestContext, cancelRequests := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRequests()
	for _, authorization := range []string{"", "Bearer jwt-looking-value", "Bearer " + token} {
		request, err := http.NewRequestWithContext(requestContext, http.MethodPost, server.URL+agentcompatCapabilityRegisterPath, strings.NewReader(requestBody))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		require.NoError(t, readErr)
		if authorization == "Bearer "+token {
			require.Equal(t, http.StatusOK, response.StatusCode)
			var envelope model.CommonResponse[agentcompatCapabilityRegisterResponse]
			require.NoError(t, json.Unmarshal(body, &envelope))
			require.False(t, envelope.Success)
			require.NotContains(t, string(body), "999")
			continue
		}
		require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	}
}

func TestAgentcompatCapabilityRoutesRegisterWaitCancelUnregisterTypedLifecycle(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	tok, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	router := gin.New()
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	capability := registerAgentcompatCapability(t, server.URL, token, agentcompatCapabilityRegisterRequest{Purpose: "terminal", ServerID: 7})
	require.NoError(t, rpc.NezhaHandlerSingleton.CreateStreamWithPurpose("private-stream", userID, 7, rpc.PurposeTerminal))
	parsed, err := rpc.ParseAgentCompatIOStreamCapability(capability)
	require.NoError(t, err)
	require.NoError(t, rpc.NezhaHandlerSingleton.BindAgentCompatIOStreamCapability(rpc.AgentCompatCapabilityBinding{
		AgentCompatCapabilityAccess: rpc.AgentCompatCapabilityAccess{
			Capability: parsed,
			Owner:      rpc.AgentCompatCapabilityOwner{PATID: tok.ID, UserID: userID},
			Purpose:    rpc.AgentCompatCapabilityTerminal, TargetServerID: 7,
			ServerAccessAllowed: true,
		},
		StreamID: "private-stream",
	}))
	waitContext, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	result, err := postAgentcompatCapability[agentcompatCapabilityWaitRequest, agentcompatCapabilityWaitResponse](waitContext, server.URL+agentcompatCapabilityWaitPath, token, agentcompatCapabilityWaitRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.NoError(t, err)
	require.Equal(t, "private-stream", result.StreamID)

	status, body := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityUnregisterPath, token, agentcompatCapabilityUnregisterRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, capability)

	status, body = postAgentcompatEmpty(t, server.URL+agentcompatCapabilityCancelPath, token, agentcompatCapabilityCancelRequest{Capability: capability, Purpose: "terminal", ServerID: 7})
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, capability)
}

func TestAgentcompatCapabilityRoutesWaitCancellationDoesNotLeakSecrets(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	router := gin.New()
	registerAgentcompatRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	request := agentcompatCapabilityWaitRequest{Capability: strings.Repeat("a", 43), Purpose: "terminal", ServerID: 7}
	status, body := postAgentcompatEmpty(t, server.URL+agentcompatCapabilityWaitPath, token, request)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, request.Capability)
	require.NotContains(t, body, "private-stream")
}

func registerAgentcompatCapability(t *testing.T, baseURL, token string, request agentcompatCapabilityRegisterRequest) string {
	t.Helper()
	requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := postAgentcompatCapability[agentcompatCapabilityRegisterRequest, agentcompatCapabilityRegisterResponse](requestContext, baseURL+agentcompatCapabilityRegisterPath, token, request)
	require.NoError(t, err)
	require.NotEmpty(t, response.Capability)
	return response.Capability
}

func postAgentcompatEmpty(t *testing.T, path, token string, body any) (int, string) {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, path, bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	bodyBytes, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(bodyBytes)
}

func postAgentcompatCapability[Request, Response any](ctx context.Context, path, token string, body Request) (Response, error) {
	var zero Response
	encoded, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(encoded))
	if err != nil {
		return zero, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()
	var envelope model.CommonResponse[Response]
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return zero, err
	}
	if !envelope.Success {
		return zero, errors.New(envelope.Error)
	}
	return envelope.Data, nil
}
