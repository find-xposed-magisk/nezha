package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// A tool error must not ship structuredContent: strict clients validate it
// against the tool's outputSchema (which requires exec/fs result fields) and
// reject the whole response with -32602, masking the real isError text.
func TestServerExec_ErrorResult_OmitsStructuredContent(t *testing.T) {
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
	require.Nil(t, tcr.StructuredContent,
		"error responses must omit structuredContent so strict clients don't validate it against outputSchema and mask the real error")
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
	require.Nil(t, tcr.StructuredContent,
		"scope-denied error must also omit structuredContent")
}
