package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// 在 /api/v1 风格的小 router 上重现 PAT + scope mw，验证 enforcement。
func setupRESTScopeServer(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	cleanupBase, uid := setupMCPTest(t)

	_, plain := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	pat := apiTokenAuthMiddleware()
	r.GET("/api/v1/server",
		pat,
		restScopeMiddleware(model.ScopeServerRead),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	r.POST("/api/v1/server/config",
		pat,
		restScopeMiddleware(model.ScopeServerWrite),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	r.GET("/api/v1/profile",
		pat,
		restPATForbiddenMiddleware(),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)

	ts := httptest.NewServer(r)
	return ts, plain, func() {
		ts.Close()
		cleanupBase()
	}
}

func httpGetWithToken(t *testing.T, ts *httptest.Server, path, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func httpPostWithToken(t *testing.T, ts *httptest.Server, path, token string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader("{}"))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func TestRESTScope_PATWithReadCanGET(t *testing.T) {
	ts, tok, cleanup := setupRESTScopeServer(t)
	defer cleanup()
	code, body := httpGetWithToken(t, ts, "/api/v1/server", tok)
	require.Equal(t, 200, code)
	require.True(t, body["ok"].(bool))
}

func TestRESTScope_PATWithReadCannotWrite(t *testing.T) {
	ts, tok, cleanup := setupRESTScopeServer(t)
	defer cleanup()
	code, body := httpPostWithToken(t, ts, "/api/v1/server/config", tok)
	require.Equal(t, 403, code)
	require.Contains(t, body["error"], "nezha:server:write")
}

func TestRESTScope_NoTokenIsTransparentToScopeMW(t *testing.T) {
	ts, _, cleanup := setupRESTScopeServer(t)
	defer cleanup()
	code, _ := httpGetWithToken(t, ts, "/api/v1/server", "")
	require.Equal(t, 200, code, "scope mw is PAT-only enforcement; JWT flow is gated by jwtOrPATAuthMiddleware before this layer. In this minimal router there is no JWT mw, so no token = handler runs (security is enforced upstream)")
}

func TestRESTScope_PATForbiddenOnSelfManagement(t *testing.T) {
	ts, tok, cleanup := setupRESTScopeServer(t)
	defer cleanup()
	code, body := httpGetWithToken(t, ts, "/api/v1/profile", tok)
	require.Equal(t, 403, code)
	require.Contains(t, body["error"], "not accessible by api token")
}

func TestRESTScope_JWTUserSkipsScope(t *testing.T) {
	cleanupBase, uid := setupMCPTest(t)
	defer cleanupBase()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	pat := apiTokenAuthMiddleware()
	r.GET("/api/v1/server",
		pat,
		func(c *gin.Context) {
			c.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: uid}, Role: model.RoleMember})
			c.Next()
		},
		restScopeMiddleware(model.ScopeServerRead),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	code, body := httpGetWithToken(t, ts, "/api/v1/server", "")
	require.Equal(t, 200, code, "JWT-attached request (no PAT in ctx) must bypass scope check")
	require.True(t, body["ok"].(bool))
}

func TestRESTScope_NezhaAllUnlocksEverything(t *testing.T) {
	cleanupBase, uid := setupMCPTest(t)
	defer cleanupBase()
	_, plain := mkToken(t, uid, []string{model.ScopeNezhaAll}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	pat := apiTokenAuthMiddleware()
	r.POST("/api/v1/server/config",
		pat,
		restScopeMiddleware(model.ScopeServerWrite),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	r.POST("/api/v1/batch-delete/server",
		pat,
		restScopeMiddleware(model.ScopeServerDelete),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	for _, path := range []string{"/api/v1/server/config", "/api/v1/batch-delete/server"} {
		code, _ := httpPostWithToken(t, ts, path, plain)
		require.Equalf(t, 200, code, "nezha:* must unlock %s", path)
	}
}

// --- WAF brute force ---

func TestRESTScope_BadPATIncrementsWAFCounter(t *testing.T) {
	cleanupBase, _ := setupMCPTest(t)
	defer cleanupBase()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(model.CtxKeyRealIPStr, "203.0.113.7")
		c.Next()
	})
	r.GET("/api/v1/server",
		apiTokenAuthMiddleware(),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	for i := 0; i < 3; i++ {
		code, _ := httpGetWithToken(t, ts, "/api/v1/server", "nzp_invalid_token_xxx")
		require.Equal(t, 401, code)
	}

	var w model.WAF
	require.NoError(t, singleton.DB.Where("block_identifier = ?", model.BlockIDToken).First(&w).Error)
	require.GreaterOrEqual(t, w.Count, uint64(3))
}

func TestRESTScope_GoodPATClearsWAFCounter(t *testing.T) {
	cleanupBase, uid := setupMCPTest(t)
	defer cleanupBase()

	_, plain := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(model.CtxKeyRealIPStr, "198.51.100.5")
		c.Next()
	})
	r.GET("/api/v1/server",
		apiTokenAuthMiddleware(),
		restScopeMiddleware(model.ScopeServerRead),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	_, _ = httpGetWithToken(t, ts, "/api/v1/server", "nzp_invalid_token_xxx")
	var w model.WAF
	require.NoError(t, singleton.DB.Where("block_identifier = ?", model.BlockIDToken).First(&w).Error)
	require.GreaterOrEqual(t, w.Count, uint64(1))

	code, _ := httpGetWithToken(t, ts, "/api/v1/server", plain)
	require.Equal(t, 200, code)
	require.ErrorContains(t,
		singleton.DB.Where("block_identifier = ?", model.BlockIDToken).First(&model.WAF{}).Error,
		"record not found",
	)
}
