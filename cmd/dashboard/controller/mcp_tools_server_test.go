package controller

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestServerList_FiltersByPermission(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	srv2 := &model.Server{}
	srv2.ID = 8
	srv2.Name = "beta"
	srv2.SetUserID(999)
	singleton.ServerShared.InsertForTest(srv2)

	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{Name: "server.list", Arguments: json.RawMessage("{}")}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.False(t, tcr.IsError)

	rb, _ := json.Marshal(tcr.StructuredContent)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rb, &rows))
	require.Len(t, rows, 1, "must filter out non-owned server")
	require.EqualValues(t, 7, rows[0]["id"])
}

func TestServerList_ServerWhitelistFurtherFiltering(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	srv2 := &model.Server{}
	srv2.ID = 8
	srv2.Name = "beta"
	srv2.SetUserID(uid)
	singleton.ServerShared.InsertForTest(srv2)

	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, []uint64{8})
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{Name: "server.list", Arguments: json.RawMessage("{}")}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.False(t, tcr.IsError)
	rb, _ := json.Marshal(tcr.StructuredContent)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rb, &rows))
	require.Len(t, rows, 1)
	require.EqualValues(t, 8, rows[0]["id"])
}

func TestServerList_OnlineOnlyFilter(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	srv, _ := singleton.ServerShared.Get(7)
	require.NotNil(t, srv)
	srv.LastActive = time.Now()

	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{Name: "server.list", Arguments: jsonRaw(map[string]any{"online_only": true})}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.False(t, tcr.IsError)
	rb, _ := json.Marshal(tcr.StructuredContent)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rb, &rows))
	require.Len(t, rows, 1)
}

func TestServerGet_RequiresServerID(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{Name: "server.get", Arguments: json.RawMessage("{}")}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.True(t, tcr.IsError)
	require.Contains(t, tcr.Content[0].Text, "server_id required")
}

func TestServerExec_ScopeMissing(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{Name: "server.exec", Arguments: jsonRaw(map[string]any{"server_id": 7, "cmd": "echo"})}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.True(t, tcr.IsError)
	require.Contains(t, tcr.Content[0].Text, "nezha:server:exec")
}
