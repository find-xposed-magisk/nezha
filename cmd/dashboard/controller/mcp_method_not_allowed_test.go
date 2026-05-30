package controller

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// Streamable HTTP 规范（modelcontextprotocol.io /basic/transports）要求：
// 服务端如果不提供 standalone SSE，必须对 GET /mcp 返回 405 Method Not Allowed。
// 现状是 Gin 的 NoRoute fallback 会把 GET /mcp 喂给前端 fallback（HTML/404），
// 真实 MCP 客户端在自动探测 SSE 时会卡住或拿到无效内容。
//
// 这条测试拼出和生产 routers() 一致的 /mcp 三件套，仅断言「非 POST 不返回 HTML」。
type mcpFallbackDist struct{}

func (mcpFallbackDist) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

func setupMCPMethodRouter(t *testing.T) *gin.Engine {
	t.Helper()
	originalConf := singleton.Conf
	singleton.Conf = &singleton.ConfigClass{Config: &model.Config{
		ConfigDashboard: model.ConfigDashboard{
			AdminTemplate: "admin-dist",
			UserTemplate:  "user-dist",
		},
	}}
	t.Cleanup(func() { singleton.Conf = originalConf })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/mcp", mcpOriginGuard(), apiTokenAuthMiddleware(), mcpEndpoint)
	r.GET("/mcp", mcpMethodNotAllowed)
	r.DELETE("/mcp", mcpMethodNotAllowed)
	r.GET("/mcp/download/:token", mcpOriginGuard(), transferDownloadHandler)
	r.POST("/mcp/upload/:token", mcpOriginGuard(), transferUploadHandler)
	r.NoRoute(fallbackToFrontend(mcpFallbackDist{}))
	return r
}

func TestMCP_GetReturnsMethodNotAllowed(t *testing.T) {
	t.Chdir(t.TempDir())
	r := setupMCPMethodRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp must return 405 per Streamable HTTP spec; got %d body=%q",
			w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "<html") {
		t.Fatalf("GET /mcp must not fall back to the SPA index.html; body=%q", w.Body.String())
	}
}

func TestMCP_DeleteReturnsMethodNotAllowed(t *testing.T) {
	t.Chdir(t.TempDir())
	r := setupMCPMethodRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /mcp (session terminate) must return 405 when sessions are not implemented; got %d",
			w.Code)
	}
}
