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

func newCronListPATRouter(t *testing.T, tok *model.APIToken) *gin.Engine {
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
	r.GET("/api/v1/cron", listHandler(listCron))
	return r
}

// GET /api/v1/cron must replay the same deny-list rule the dispatch guards
// use; otherwise a stale or out-of-band-written CronCoverAll row whose
// Servers deny-list does not cover the non-whitelisted owner server still
// shows up in the limited PAT's list view.
func TestListCron_HidesCoverAllWithInsufficientDenyForLimitedPAT(t *testing.T) {
	setupCronDispatchPATFixture(t)
	insufficient := insertCronForDispatchTest(t, model.CronCoverAll, []uint64{1})
	sufficient := insertCronForDispatchTest(t, model.CronCoverAll, []uint64{2})

	tok := &model.APIToken{ID: 23, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronListPATRouter(t, tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cron", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Success bool          `json:"success"`
		Error   string        `json:"error"`
		Data    []*model.Cron `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success, resp.Error)

	seen := map[uint64]bool{}
	for _, c := range resp.Data {
		seen[c.ID] = true
	}
	assert.False(t, seen[insufficient],
		"PAT [1] must NOT see a CronCoverAll whose deny-list does not cover owner server 2 (rows=%+v)", resp.Data)
	assert.True(t, seen[sufficient],
		"PAT [1] must still see a CronCoverAll whose deny-list already covers every non-whitelisted owner server")
}
