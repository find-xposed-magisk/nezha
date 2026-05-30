package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func TestREST_ServerConfigRequiresWriteScope(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	_, plain := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/server/config/:id",
		apiTokenAuthMiddleware(),
		restScopeMiddleware(serverConfigSensitiveScope()),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp := doReq(t, ts, "GET", "/api/v1/server/config/7", plain)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"nezha:server:read must not be sufficient to read agent config (contains client_secret)")
}

func TestREST_ServerConfigGrantedByWriteScope(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	_, plain := mkToken(t, uid, []string{model.ScopeServerWrite}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/server/config/:id",
		apiTokenAuthMiddleware(),
		restScopeMiddleware(serverConfigSensitiveScope()),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) },
	)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp := doReq(t, ts, "GET", "/api/v1/server/config/7", plain)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
