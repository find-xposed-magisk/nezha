package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func newPATCtxSetter(callerID uint64, role model.Role, tok *model.APIToken) gin.HandlerFunc {
	return func(c *gin.Context) {
		setAuthUser(c, callerID, role)
		if tok != nil {
			c.Set(model.CtxKeyAPIToken, tok)
			c.Set(apiTokenCtxKey, tok)
		}
		c.Next()
	}
}

func callListTransferWithPAT(t *testing.T, callerID uint64, tok *model.APIToken) ([]*model.ServerTransfer, bool, string) {
	t.Helper()
	r := gin.New()
	r.Use(newPATCtxSetter(callerID, model.RoleMember, tok))
	r.GET("/transfer", listHandler(listServerTransfer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/transfer", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Success bool                    `json:"success"`
		Error   string                  `json:"error"`
		Data    []*model.ServerTransfer `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data, resp.Success, resp.Error
}

func callCancelTransferWithPAT(t *testing.T, transferID, callerID uint64, tok *model.APIToken) (commonResponseShape, int) {
	t.Helper()
	r := gin.New()
	r.Use(newPATCtxSetter(callerID, model.RoleMember, tok))
	r.POST("/transfer/:id/cancel", commonHandler(cancelServerTransfer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/transfer/"+strconv.FormatUint(transferID, 10)+"/cancel",
		bytes.NewReader(nil))
	r.ServeHTTP(w, req)

	var resp commonResponseShape
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp, w.Code
}

func TestListServerTransfer_HidesRowsForServersOutsidePATWhitelist(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()
	seedServer(t, 1, 100)
	seedServer(t, 2, 100)
	insideID := seedPendingTransfer(t, 1, 100, 200, 100)
	outsideID := seedPendingTransfer(t, 2, 100, 200, 100)

	tok := &model.APIToken{ID: 17, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	rows, ok, errStr := callListTransferWithPAT(t, 100, tok)
	assert.True(t, ok, "list call must succeed: %s", errStr)

	seen := map[uint64]bool{}
	for _, r := range rows {
		seen[r.ID] = true
	}
	assert.True(t, seen[insideID],
		"transfer of whitelisted server 1 must still be visible (got %d rows)", len(rows))
	assert.False(t, seen[outsideID],
		"transfer of non-whitelisted server 2 must be hidden from PAT view (rows=%+v)", rows)
}

func TestCancelServerTransfer_DeniesServerOutsidePATWhitelist(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()
	seedServer(t, 1, 100)
	seedServer(t, 2, 100)
	_ = seedPendingTransfer(t, 1, 100, 200, 100)
	outsideID := seedPendingTransfer(t, 2, 100, 200, 100)

	tok := &model.APIToken{ID: 17, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	resp, status := callCancelTransferWithPAT(t, outsideID, 100, tok)
	assert.Equal(t, http.StatusOK, status)
	assert.False(t, resp.Success,
		"PAT whitelist [1] must not allow cancelling transfer of server 2 (FromUserID match alone is not enough)")
	assert.Contains(t, resp.Error, "permission denied")
}

// admin PAT 同样必须受 server_ids 收窄：admin 给自己签的 PAT 加上 ServerIDs={1}
// 后，列表/取消都不能再触达白名单外的 server。这是修复 ServerTransfer.HasPermission
// 在 admin 早返回前未检查 PAT 的回归用例。
func callListTransferWithAdminPAT(t *testing.T, callerID uint64, tok *model.APIToken) ([]*model.ServerTransfer, bool, string) {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, callerID, model.RoleAdmin)
		if tok != nil {
			c.Set(model.CtxKeyAPIToken, tok)
			c.Set(apiTokenCtxKey, tok)
		}
		c.Next()
	})
	r.GET("/transfer", listHandler(listServerTransfer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/transfer", nil)
	r.ServeHTTP(w, req)
	var resp struct {
		Success bool                    `json:"success"`
		Error   string                  `json:"error"`
		Data    []*model.ServerTransfer `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data, resp.Success, resp.Error
}

func TestListServerTransfer_AdminPATIsAlsoNarrowedByWhitelist(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()
	seedServer(t, 1, 100)
	seedServer(t, 2, 100)
	insideID := seedPendingTransfer(t, 1, 100, 200, 100)
	outsideID := seedPendingTransfer(t, 2, 100, 200, 100)

	tok := &model.APIToken{ID: 18, UserID: 999}
	tok.SetServerIDs([]uint64{1})

	rows, ok, errStr := callListTransferWithAdminPAT(t, 999, tok)
	assert.True(t, ok, "list call must succeed: %s", errStr)

	seen := map[uint64]bool{}
	for _, r := range rows {
		seen[r.ID] = true
	}
	assert.True(t, seen[insideID], "admin PAT scoped to {1} must still see transfer of server 1")
	assert.False(t, seen[outsideID],
		"admin PAT scoped to {1} must NOT see transfer of server 2 (admin early-return is no longer a bypass)")
}
