//go:build agentcompat

package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func TestAgentcompatFsWriteContractRejectsCallerPayloadAndForeignServer(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	_, plaintext := mkToken(t, userID, []string{model.ScopeServerWrite}, []uint64{99})
	server := newAgentcompatCapabilityServer(t)

	status, body := postAgentcompatRaw(t, server.URL+"/agentcompat/fs-write-contract", plaintext, `{"server_id":7,"path":"/tmp/foreign","operation":"oversize","content":"attacker"}`)

	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, "attacker")
	require.Contains(t, body, "unknown field")

	status, body = postAgentcompatRaw(t, server.URL+"/agentcompat/fs-write-contract", plaintext, `{"server_id":7,"operation":"oversize"}`)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "permission denied")
}

func TestAgentcompatFsWriteContractRequiresWriteScopeBeforeRPC(t *testing.T) {
	cleanup, userID := setupMCPTest(t)
	t.Cleanup(cleanup)
	_, plaintext := mkToken(t, userID, []string{model.ScopeServerRead}, nil)
	server := newAgentcompatCapabilityServer(t)

	status, body := postAgentcompatRaw(t, server.URL+"/agentcompat/fs-write-contract", plaintext, `{"server_id":7,"operation":"oversize"}`)

	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "missing required scope")
	require.NotContains(t, body, `"agent_rpc_response":true`)
}

func TestAgentcompatProbeJSONRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	tests := []string{
		`{"server_id":7,"operation":"oversize","path":"caller"}`,
		`{"server_id":7,"operation":"oversize","content":"caller"}`,
		`{"server_id":7,"operation":"oversize"}{}`,
	}
	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			context := newAgentcompatJSONContext(t, body)
			var request agentcompatFsWriteContractRequest
			require.Error(t, decodeAgentcompatJSON(context, &request))
		})
	}
}

func newAgentcompatJSONContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	context.Request.Header.Set("Content-Type", "application/json")
	return context
}
