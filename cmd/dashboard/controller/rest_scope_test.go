package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// setupRESTScopeTest 准备一个 PAT + 一个最小路由表，用于测 REST scope enforce。
func setupRESTScopeTest(t *testing.T) (*httptest.Server, *model.APIToken, string, func()) {
	t.Helper()
	cleanupBase, uid := setupMCPTest(t)

	tok, plain := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	patMw := apiTokenAuthMiddleware()

	r.GET("/server",
		patMw,
		restScopeMiddleware(model.ScopeServerRead),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	r.POST("/server/config",
		patMw,
		restScopeMiddleware(model.ScopeServerWrite),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	r.POST("/server-group",
		patMw,
		restScopeMiddleware(model.ScopeServerWrite),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	r.GET("/api-tokens",
		patMw,
		restPATForbiddenMiddleware(),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	return ts, tok, plain, func() {
		ts.Close()
		cleanupBase()
	}
}

func doReq(t *testing.T, ts *httptest.Server, method, path, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader([]byte("{}")))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestREST_PATWithMatchingScopeAllowed(t *testing.T) {
	ts, _, tok, cleanup := setupRESTScopeTest(t)
	defer cleanup()
	resp := doReq(t, ts, "GET", "/server", tok)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestREST_PATWithoutScopeDenied(t *testing.T) {
	ts, _, tok, cleanup := setupRESTScopeTest(t)
	defer cleanup()
	resp := doReq(t, ts, "POST", "/server/config", tok)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	var body model.CommonResponse[any]
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.False(t, body.Success)
	require.Contains(t, body.Error, "nezha:server:write")
}

func TestREST_SelfManagementForbidsPAT(t *testing.T) {
	ts, _, tok, cleanup := setupRESTScopeTest(t)
	defer cleanup()
	resp := doReq(t, ts, "GET", "/api-tokens", tok)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestREST_PATWildcardCoversAllVerbs(t *testing.T) {
	cleanupBase, uid := setupMCPTest(t)
	defer cleanupBase()

	tok, plain := mkToken(t, uid, []string{"nezha:server:*"}, nil)
	_ = tok

	gin.SetMode(gin.TestMode)
	r := gin.New()
	patMw := apiTokenAuthMiddleware()
	r.GET("/server", patMw, restScopeMiddleware(model.ScopeServerRead),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.POST("/server/config", patMw, restScopeMiddleware(model.ScopeServerWrite),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.POST("/batch-delete/server", patMw, restScopeMiddleware(model.ScopeServerDelete),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	ts := httptest.NewServer(r)
	defer ts.Close()

	for _, tc := range []struct {
		method, path string
	}{
		{"GET", "/server"},
		{"POST", "/server/config"},
		{"POST", "/batch-delete/server"},
	} {
		resp := doReq(t, ts, tc.method, tc.path, plain)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s %s should be allowed by nezha:server:*", tc.method, tc.path)
	}
}

func TestREST_NezhaAllGrantsEverything(t *testing.T) {
	cleanupBase, uid := setupMCPTest(t)
	defer cleanupBase()

	_, plain := mkToken(t, uid, []string{model.ScopeNezhaAll}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/maintenance",
		apiTokenAuthMiddleware(),
		restScopeMiddleware(model.ScopeAdminAll),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp := doReq(t, ts, "POST", "/maintenance", plain)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestREST_NoAuthGoesToJWTChain(t *testing.T) {
	cleanupBase, _ := setupMCPTest(t)
	defer cleanupBase()

	jwtCalled := false
	fakeJwt := func(c *gin.Context) {
		jwtCalled = true
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "no jwt"})
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/server",
		jwtOrPATAuthMiddleware(apiTokenAuthMiddleware(), fakeJwt),
		restScopeMiddleware(model.ScopeServerRead),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp := doReq(t, ts, "GET", "/server", "")
	resp.Body.Close()
	require.True(t, jwtCalled, "JWT mw must be invoked when no PAT")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestREST_BadPATShortCircuitsBeforeJWT(t *testing.T) {
	cleanupBase, _ := setupMCPTest(t)
	defer cleanupBase()

	jwtCalled := false
	fakeJwt := func(c *gin.Context) { jwtCalled = true }
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(model.CtxKeyRealIPStr, "203.0.113.99")
		c.Next()
	})
	r.GET("/server",
		jwtOrPATAuthMiddleware(apiTokenAuthMiddleware(), fakeJwt),
		restScopeMiddleware(model.ScopeServerRead),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp := doReq(t, ts, "GET", "/server", "nzp_bogus_token_value")
	resp.Body.Close()
	require.False(t, jwtCalled, "JWT mw must NOT run after bad PAT abort")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var blocked model.WAF
	err := singleton.DB.Where("block_identifier = ?", model.BlockIDToken).First(&blocked).Error
	require.NoError(t, err, "bad PAT must trigger WAF BlockIP")
	require.GreaterOrEqual(t, blocked.Count, uint64(1))
}
