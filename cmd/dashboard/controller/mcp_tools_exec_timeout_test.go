package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func TestServerExec_RejectsOutOfRangeTimeoutSeconds(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	tok, _ := mkToken(t, uid, []string{model.ScopeServerExec}, nil)

	// timeout_seconds is documented as 1..300 in the tool schema; sending
	// 1_000_000 would otherwise let the dashboard wait ~1e6s on rpc.CallAgent
	// when the agent is unreachable / old, turning one MCP call into a
	// long-lived goroutine + connection occupation. Handler must reject
	// before touching the RPC layer.
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{
			Name: "server.exec",
			Arguments: jsonRaw(map[string]any{
				"server_id":       7,
				"cmd":             "echo",
				"timeout_seconds": 1_000_000,
			}),
		}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.NotNil(t, tcr)
	require.True(t, tcr.IsError, "expected isError=true for out-of-range timeout, got %+v", tcr)
	require.Contains(t, tcr.Content[0].Text, "timeout_seconds")
}

func TestServerExec_RejectsZeroLikeNegativeTimeoutBoundary(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	tok, _ := mkToken(t, uid, []string{model.ScopeServerExec}, nil)

	// 301s sits one above the documented maximum. The previous handler
	// happily forwarded it as-is and added +5s to the dashboard-side wait,
	// so any client could ignore the schema bound. Pin the rejection.
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{
			Name: "server.exec",
			Arguments: jsonRaw(map[string]any{
				"server_id":       7,
				"cmd":             "echo",
				"timeout_seconds": 301,
			}),
		}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.True(t, tcr.IsError)
	require.Contains(t, tcr.Content[0].Text, "timeout_seconds")
}
