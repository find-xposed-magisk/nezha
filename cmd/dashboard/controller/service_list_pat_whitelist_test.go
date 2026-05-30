package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func newServiceListPATRouter(t *testing.T, tok *model.APIToken) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, 100, model.RoleMember)
		if tok != nil {
			c.Set(model.CtxKeyAPIToken, tok)
			c.Set(apiTokenCtxKey, tok)
		}
		c.Next()
	})
	r.GET("/api/v1/service/list", listHandler(listService))
	return r
}

// GET /api/v1/service/list must hide ServiceCoverAll rows whose SkipServers
// deny-set does not cover every owner server outside the PAT whitelist.
// DispatchTask would still probe those servers, so leaking the row to the
// list view (and exposing target/credentials/triggers) is a real PAT scope
// escape.
func TestListService_HidesCoverAllWithInsufficientSkipForLimitedPAT(t *testing.T) {
	setupServiceDispatchPATFixture(t)
	insufficient := insertServiceForDispatchTest(t, model.ServiceCoverAll, map[uint64]bool{1: true})
	sufficient := insertServiceForDispatchTest(t, model.ServiceCoverAll, map[uint64]bool{2: true})

	tok := &model.APIToken{ID: 34, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newServiceListPATRouter(t, tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/service/list", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Success bool             `json:"success"`
		Error   string           `json:"error"`
		Data    []*model.Service `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success, resp.Error)

	seen := map[uint64]bool{}
	for _, s := range resp.Data {
		seen[s.ID] = true
	}
	assert.False(t, seen[insufficient],
		"PAT [1] must NOT see a ServiceCoverAll whose SkipServers does not cover owner server 2 (rows=%+v)", resp.Data)
	assert.True(t, seen[sufficient],
		"PAT [1] must still see a ServiceCoverAll whose SkipServers already covers every non-whitelisted owner server")
}
