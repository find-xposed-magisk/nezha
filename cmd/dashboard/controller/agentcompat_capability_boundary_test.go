//go:build agentcompat

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
)

func TestAgentcompatCapabilityBoundaryAcceptsExactly512Bytes(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)

	body := padAgentcompatCapabilityBody(t, `{"purpose":"terminal","server_id":7}`)
	status, responseBody := postAgentcompatBoundary(t, server.URL+agentcompatCapabilityRegisterPath, token, body, true)
	require.Equal(t, http.StatusOK, status)
	var envelope model.CommonResponse[agentcompatCapabilityRegisterResponse]
	require.NoError(t, json.Unmarshal([]byte(responseBody), &envelope))
	require.True(t, envelope.Success)
	require.NotEmpty(t, envelope.Data.Capability)
}

func TestAgentcompatCapabilityBoundaryRejects513BytesWithoutDisclosure(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)
	body := padAgentcompatCapabilityBody(t, `{"purpose":"terminal","server_id":7}`) + " "

	for _, route := range []struct {
		name        string
		path        string
		wantError   bool
		wantSuccess bool
	}{
		{name: "register", path: agentcompatCapabilityRegisterPath, wantError: true},
		{name: "wait", path: agentcompatCapabilityWaitPath, wantError: true},
		{name: "cancel", path: agentcompatCapabilityCancelPath, wantSuccess: true},
		{name: "unregister", path: agentcompatCapabilityUnregisterPath, wantSuccess: true},
	} {
		t.Run(route.name, func(t *testing.T) {
			status, responseBody := postAgentcompatBoundary(t, server.URL+route.path, token, body, true)
			require.Equal(t, http.StatusOK, status)
			require.NotContains(t, responseBody, "terminal")
			require.NotContains(t, responseBody, "server_id")
			if route.wantError {
				var envelope model.CommonResponse[agentcompatCapabilityRegisterResponse]
				require.NoError(t, json.Unmarshal([]byte(responseBody), &envelope))
				require.False(t, envelope.Success)
				require.Equal(t, errAgentcompatCapabilityInvalid.Error(), envelope.Error)
				return
			}
			require.Equal(t, `{"success":true,"data":{}}`, responseBody)
		})
	}
}

func TestAgentcompatCapabilityBoundaryRejectsChunkedOversizeWithoutDisclosure(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)
	body := padAgentcompatCapabilityBody(t, `{"purpose":"terminal","server_id":7}`) + " "

	status, responseBody := postAgentcompatBoundary(t, server.URL+agentcompatCapabilityRegisterPath, token, body, false)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, responseBody, errAgentcompatCapabilityInvalid.Error())
	require.NotContains(t, responseBody, "terminal")
	require.NotContains(t, responseBody, "server_id")
}

func TestAgentcompatCapabilityBoundaryRejectsDuplicateKeysInEitherOrder(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)

	keys := []string{"purpose", "server_id", "resource_id"}
	for _, key := range keys {
		for _, body := range []string{
			`{"` + key + `":"terminal","` + key + `":"terminal","purpose":"terminal","server_id":7}`,
			`{"` + key + `":0,"purpose":"terminal","server_id":7,"` + key + `":"terminal"}`,
		} {
			status, responseBody := postAgentcompatBoundary(t, server.URL+agentcompatCapabilityRegisterPath, token, body, true)
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, responseBody, errAgentcompatCapabilityInvalid.Error())
			require.NotContains(t, responseBody, "terminal")
		}
	}
}

func TestAgentcompatCapabilityBoundaryRejectsAccessGrammar(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)

	cases := []string{
		`{"capability":"x","purpose":"terminal"}`,
		`{"capability":"x","server_id":7}`,
		`{"capability":"x","purpose":"terminal","server_id":7,"capability":"y"}`,
		`{"capability":"x","purpose":"terminal","server_id":0}`,
		`{"capability":"x","purpose":"terminal","server_id":7,"unknown":"private"}`,
		`{"capability":"x","purpose":"terminal","server_id":7} {}`,
		`{"capability":null,"purpose":"terminal","server_id":7}`,
		`{"capability":7,"purpose":"terminal","server_id":7}`,
		`{"capability":"x","purpose":7,"server_id":7}`,
		`{"capability":"x","purpose":"terminal","server_id":"7"}`,
		`{"capability":"x","purpose":"terminal","server_id":-1}`,
		`{"capability":"x","purpose":"terminal","server_id":1.5}`,
		`{"capability":"x","purpose":"terminal","server_id":1e2}`,
		`null`, `[]`, `{`,
	}
	for _, body := range cases {
		status, responseBody := postAgentcompatBoundary(t, server.URL+agentcompatCapabilityWaitPath, token, body, true)
		require.Equal(t, http.StatusOK, status)
		require.Contains(t, responseBody, errAgentcompatCapabilityInvalid.Error())
		require.NotContains(t, responseBody, "private")
		require.NotContains(t, responseBody, "x")
	}
}

func TestAgentcompatCapabilityBoundaryRejectsMissingRegisterFields(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })
	_, token := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)

	for _, body := range []string{`{"server_id":7}`, `{"purpose":"terminal"}`, `{}`} {
		status, responseBody := postAgentcompatBoundary(t, server.URL+agentcompatCapabilityRegisterPath, token, body, true)
		require.Equal(t, http.StatusOK, status)
		require.Contains(t, responseBody, errAgentcompatCapabilityInvalid.Error())
		require.NotContains(t, responseBody, "terminal")
	}
}

func padAgentcompatCapabilityBody(t *testing.T, body string) string {
	t.Helper()
	require.LessOrEqual(t, len(body), 512)
	return body + strings.Repeat(" ", 512-len(body))
}

func postAgentcompatBoundary(t *testing.T, path, token, body string, contentLength bool) (int, string) {
	t.Helper()
	requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reader io.Reader = bytes.NewBufferString(body)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, path, reader)
	require.NoError(t, err)
	if !contentLength {
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = -1
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(responseBody)
}
