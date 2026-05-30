package controller

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func patRequestCtx(t *testing.T, tok *model.APIToken, uid uint64, method, path string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	c.Request = httptest.NewRequest(method, path, rdr)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: uid}, Role: model.RoleMember})
	c.Set(apiTokenCtxKey, tok)
	c.Set(model.CtxKeyAPIToken, tok)
	return c, w
}

func TestREST_PATServerWhitelistBlocksOtherServer(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, []uint64{99})

	srv, _ := singleton.ServerShared.Get(7)
	require.NotNil(t, srv)
	require.Equal(t, uid, srv.GetUserID())

	c, _ := patRequestCtx(t, tok, uid, "GET", "/api/v1/server/config/7", nil)
	c.Params = gin.Params{{Key: "id", Value: "7"}}

	_, err := getServerConfig(c)
	require.Error(t, err, "PAT not in server whitelist must be rejected")
}

func TestREST_PATServerWhitelistBlocksSetConfig(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	tok, _ := mkToken(t, uid, []string{model.ScopeServerWrite}, []uint64{99})

	c, _ := patRequestCtx(t, tok, uid, "POST", "/api/v1/server/config", model.ServerConfigForm{
		Servers: []uint64{7},
		Config:  "{}",
	})
	_, err := setServerConfig(c)
	require.Error(t, err, "setServerConfig must reject non-whitelisted server")
}

func TestREST_PATServerWhitelistAllowsListedServer(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, []uint64{7})

	c, _ := patRequestCtx(t, tok, uid, "GET", "/api/v1/server/config/7", nil)
	c.Params = gin.Params{{Key: "id", Value: "7"}}

	data, err := getServerConfig(c)
	require.NoError(t, err)
	require.Equal(t, "", data, "no agent stream connected so handler should return empty")
}
