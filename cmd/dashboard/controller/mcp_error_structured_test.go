package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestServerExec_ErrorResult_PreservesStructuredContent(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	srv, _ := singleton.ServerShared.Get(7)
	srv.SetTaskStream(&execErrorStream{errMsg: "agent disabled command execution"})

	tok, _ := mkToken(t, uid, []string{model.ScopeServerExec}, nil)

	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{
			Name: "server.exec",
			Arguments: jsonRaw(map[string]any{
				"server_id":       7,
				"cmd":             "whoami",
				"timeout_seconds": 2,
			}),
		}),
	})
	mcpEndpoint(c)

	_, tcr := decodeRPC(w)
	require.NotNil(t, tcr)
	require.True(t, tcr.IsError)
	require.Contains(t, tcr.Content[0].Text, "agent disabled command execution",
		"error text must carry the real cause")
	var result model.ExecResult
	structuredJSON, err := json.Marshal(tcr.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(structuredJSON, &result))
	require.Equal(t, -1, result.ExitCode)
	require.Equal(t, "agent disabled command execution", result.Error)
}

func TestScopeDenied_OmitsStructuredContent(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{
			Name:      "server.exec",
			Arguments: jsonRaw(map[string]any{"server_id": 7, "cmd": "echo"}),
		}),
	})
	mcpEndpoint(c)

	_, tcr := decodeRPC(w)
	require.NotNil(t, tcr)
	require.True(t, tcr.IsError)
	require.Nil(t, tcr.StructuredContent)
}
