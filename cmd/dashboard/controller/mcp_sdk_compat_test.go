package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// MCP 协议兼容性集成测试：用 modelcontextprotocol/go-sdk 官方 Go MCP client
// 对 dashboard /mcp 跑完整 initialize + tools/list + tools/call。
// 协议层用官方 SDK 严格编解码 — 任何与 MCP spec 的偏差都会被立即报错。

type sdkPATRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (rt *sdkPATRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+rt.token)
	return rt.base.RoundTrip(req)
}

func sdkTransport(endpoint, token string) *mcp.StreamableClientTransport {
	return &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: &sdkPATRoundTripper{base: http.DefaultTransport, token: token},
			Timeout:   5 * time.Second,
		},
		// /mcp 当前只实现 POST 半边 Streamable HTTP；GET SSE 通道未实现也不计划
		// 短期内上线（不需要 server→client 主动推送）。SDK 默认会试图发 GET，
		// 关掉 standalone SSE 即可严格互通。
		DisableStandaloneSSE: true,
	}
}

func setupSDKCompat(t *testing.T) (string, string, func()) {
	t.Helper()
	cleanupBase, uid := setupMCPTest(t)

	srv, _ := singleton.ServerShared.Get(7)
	srv.SetTaskStream(&e2eStream{dispatch: agentSim})

	_, plain := mkToken(t, uid, []string{
		model.ScopeInventoryRead,
		model.ScopeInventoryDelete,
		model.ScopeServerRead,
		model.ScopeServerWrite,
		model.ScopeServerDelete,
		model.ScopeServerExec,
	}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/mcp", apiTokenAuthMiddleware(), mcpEndpoint)
	ts := httptest.NewServer(r)
	return ts.URL + "/mcp", plain, func() {
		ts.Close()
		cleanupBase()
	}
}

func TestSDKClient_InitializeHandshake(t *testing.T) {
	endpoint, token, cleanup := setupSDKCompat(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "nezha-it", Version: "v0"}, nil)
	session, err := client.Connect(ctx, sdkTransport(endpoint, token), nil)
	require.NoError(t, err, "official Go SDK must initialize against /mcp")
	defer session.Close()
}

func TestSDKClient_ToolsList(t *testing.T) {
	endpoint, token, cleanup := setupSDKCompat(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "nezha-it", Version: "v0"}, nil)
	session, err := client.Connect(ctx, sdkTransport(endpoint, token), nil)
	require.NoError(t, err)
	defer session.Close()

	lst, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make(map[string]bool, len(lst.Tools))
	for _, tl := range lst.Tools {
		names[tl.Name] = true
	}
	for _, must := range []string{
		"meta.whoami",
		"server.list", "server.get", "server.exec",
		"fs.list", "fs.read", "fs.write", "fs.delete",
		"fs.download_url", "fs.upload_url",
	} {
		require.Truef(t, names[must], "tools/list missing %q", must)
	}
}

func TestSDKClient_Whoami(t *testing.T) {
	endpoint, token, cleanup := setupSDKCompat(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "nezha-it", Version: "v0"}, nil)
	session, err := client.Connect(ctx, sdkTransport(endpoint, token), nil)
	require.NoError(t, err)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "meta.whoami",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &payload))
	require.NotZero(t, payload["user_id"])
	require.NotEmpty(t, payload["scopes"])
}

func TestSDKClient_ServerExec(t *testing.T) {
	endpoint, token, cleanup := setupSDKCompat(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "nezha-it", Version: "v0"}, nil)
	session, err := client.Connect(ctx, sdkTransport(endpoint, token), nil)
	require.NoError(t, err)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "server.exec",
		Arguments: map[string]any{
			"server_id": 7,
			"cmd":       "echo",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "exec failed: %v", res.Content)
	tc := res.Content[0].(*mcp.TextContent)
	require.Contains(t, tc.Text, "simulated")
}

func TestSDKClient_FSLifecycle(t *testing.T) {
	endpoint, token, cleanup := setupSDKCompat(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "nezha-it", Version: "v0"}, nil)
	session, err := client.Connect(ctx, sdkTransport(endpoint, token), nil)
	require.NoError(t, err)
	defer session.Close()

	path := t.TempDir() + "/sdk.txt"
	for _, step := range []struct {
		name string
		args map[string]any
	}{
		{"fs.write", map[string]any{"server_id": 7, "path": path, "content": "via-sdk", "encoding": "utf8"}},
		{"fs.read", map[string]any{"server_id": 7, "path": path}},
		{"fs.delete", map[string]any{"server_id": 7, "path": path}},
	} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: step.name, Arguments: step.args})
		require.NoError(t, err, step.name)
		require.False(t, res.IsError, "%s failed: %v", step.name, res.Content)
	}
}

func TestSDKClient_BadPAT(t *testing.T) {
	endpoint, _, cleanup := setupSDKCompat(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "nezha-it", Version: "v0"}, nil)
	_, err := client.Connect(ctx, sdkTransport(endpoint, "nzp_invalid"), nil)
	require.Error(t, err)
}
