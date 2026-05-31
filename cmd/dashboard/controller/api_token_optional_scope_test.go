package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func setupOptionalAuthRouter(t *testing.T, plainToken string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	jwtMw := func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "no jwt"})
	}
	patMw := apiTokenAuthMiddleware()
	authMw := jwtOrPATAuthMiddleware(patMw, jwtMw)

	stub := func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) }
	optionalAuth := r.Group("/api/v1", authMw)
	optionalAuth.GET("/server-group", restScopeMiddleware(model.ScopeInventoryRead), stub)
	optionalAuth.GET("/service", restScopeMiddleware(model.ScopeServiceRead), stub)
	optionalAuth.GET("/server/:id/metrics", restScopeMiddleware(model.ScopeServerRead), stub)

	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	_ = plainToken
	return ts
}

func TestOptionalAuth_PATWithoutScopeIsDenied(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	_, plain := mkToken(t, uid, []string{model.ScopeNotificationRead}, nil)
	ts := setupOptionalAuthRouter(t, plain)

	for _, path := range []string{
		"/api/v1/server-group",
		"/api/v1/service",
		"/api/v1/server/7/metrics",
	} {
		resp := doReq(t, ts, "GET", path, plain)
		resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "PAT lacking required scope must be denied for %s", path)
	}
}

func TestOptionalAuth_PATWithMatchingScopeAllowed(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	_, plain := mkToken(t, uid, []string{model.ScopeInventoryRead, model.ScopeServerRead, model.ScopeServiceRead}, nil)
	ts := setupOptionalAuthRouter(t, plain)

	for _, path := range []string{
		"/api/v1/server-group",
		"/api/v1/service",
		"/api/v1/server/7/metrics",
	} {
		resp := doReq(t, ts, "GET", path, plain)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "PAT with matching scope must pass for %s", path)
	}
}
