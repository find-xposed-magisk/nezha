package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func setupMCPOriginRouter(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	cleanup, uid := setupMCPTest(t)
	_, plain := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/mcp", mcpOriginGuard(), apiTokenAuthMiddleware(), mcpEndpoint)
	r.GET("/mcp/download/:token", mcpOriginGuard(), transferDownloadHandler)
	r.POST("/mcp/upload/:token", mcpOriginGuard(), transferUploadHandler)
	ts := httptest.NewServer(r)
	return ts, plain, func() {
		ts.Close()
		cleanup()
	}
}

func TestMCP_DisallowsCrossOriginRequest(t *testing.T) {
	ts, tok, cleanup := setupMCPOriginRouter(t)
	defer cleanup()

	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMCP_AllowsRequestWithoutOriginHeader(t *testing.T) {
	ts, tok, cleanup := setupMCPOriginRouter(t)
	defer cleanup()

	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMCP_AllowsSameHostOrigin(t *testing.T) {
	ts, tok, cleanup := setupMCPOriginRouter(t)
	defer cleanup()

	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Origin", "http://"+req.Host)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// 公网部署回归：ListenHost 是 0.0.0.0/未指定时，前端会以公网 Host 同源访问。
// 这条以前会被 dashboardListensOnLoopback 误判为 loopback 部署进而拒掉；
// 现在必须放行，否则正常生产环境的 admin frontend MCP 入口直接 403。
func TestMCP_PublicDeployment_AllowsPublicSameOrigin(t *testing.T) {
	ts, tok, cleanup := setupMCPOriginRouter(t)
	defer cleanup()

	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Host = "dashboard.example.com"
	req.Header.Set("Origin", "https://dashboard.example.com")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// 显式绑 loopback 时仍然要执行 DNS rebinding 防线：Host 是公网域名 → 403。
func TestMCP_LoopbackDeployment_RejectsPublicHost(t *testing.T) {
	ts, tok, cleanup := setupMCPOriginRouter(t)
	defer cleanup()
	prev := singleton.Conf.ListenHost
	singleton.Conf.ListenHost = "127.0.0.1"
	defer func() { singleton.Conf.ListenHost = prev }()

	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Host = "dashboard.example.com"
	req.Header.Set("Origin", "https://dashboard.example.com")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
