package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// installTestConfig swaps singleton.Conf with one backed by a tmp file so
// updateConfig's Conf.Save() write-through has a real target. The caller's
// setupMCPTest will restore the original Conf when its cleanup runs.
func installTestConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfg := &model.Config{}
	require.NoError(t, cfg.Read(filepath.Join(dir, "config.yaml"), nil))
	singleton.Conf = &singleton.ConfigClass{Config: cfg}
}

func TestUpdateConfig_PersistsEnableMCPFlag(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	installTestConfig(t)

	origTemplates := singleton.FrontendTemplates
	singleton.FrontendTemplates = []model.FrontendTemplate{
		{Path: "user-dist", IsAdmin: false},
	}
	defer func() { singleton.FrontendTemplates = origTemplates }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, uid, model.RoleAdmin)
		c.Next()
	})
	r.PATCH("/api/v1/setting", commonHandler(updateConfig))

	body := map[string]any{
		"site_name":     "test",
		"language":      "en_US",
		"user_template": "user-dist",
		"enable_mcp":    true,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/setting", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	require.True(t, success, "PATCH /setting must succeed: %s", errMsg)
	require.True(t, singleton.Conf.EnableMCP,
		"enable_mcp=true in body must flip singleton.Conf.EnableMCP")
}

func TestMCPEndpoint_RefusesWhenDisabled(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	singleton.Conf.SetMCPEnabled(false)
	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize",
	})
	mcpEndpoint(c)
	var env jsonRPCResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	require.NotNil(t, env.Error, "MCP must return JSON-RPC error when disabled; body=%s", w.Body.String())
	require.Equal(t, rpcErrForbidden, env.Error.Code,
		"disabled MCP must surface as rpcErrForbidden so callers can distinguish from auth failure")
}

func TestMCPEndpoint_AllowsWhenEnabled(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	singleton.Conf.SetMCPEnabled(true)

	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize",
	})
	mcpEndpoint(c)
	var env jsonRPCResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	require.Nil(t, env.Error, "MCP must process requests when enabled; got error=%+v", env.Error)
}
