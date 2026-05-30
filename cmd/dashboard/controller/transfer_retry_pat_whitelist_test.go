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

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// retryServerTransfer 必须像 cancel/list 一样受 PAT 的 server_ids 白名单收窄：
// admin 给自己签的 PAT 加上 ServerIDs={1} 之后，不能再用它 retry server 2 的
// 历史 transfer 行。否则白名单只在 read/cancel 上生效，retry 路径仍是“admin
// 早返回 → 完全绕过白名单”，与 model.ServerTransfer.HasPermission 注释里
// “PAT server_ids whitelist is evaluated FIRST, before the admin short-
// circuit” 直接冲突。
func callRetryServerTransferWithAdminPAT(t *testing.T, transferID, callerID uint64, tok *model.APIToken) (commonResponseShape, int) {
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
	r.POST("/transfer/:id/retry", commonHandler(retryServerTransfer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/transfer/"+strconv.FormatUint(transferID, 10)+"/retry",
		bytes.NewReader(nil))
	r.ServeHTTP(w, req)

	var resp commonResponseShape
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp, w.Code
}

func TestRetryServerTransfer_AdminPATIsNarrowedByServerWhitelist(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()

	seedServer(t, 1, 300)
	seedServer(t, 2, 300)
	insideID := seedFailedTransfer(t, 1, 100, 200, 100)
	outsideID := seedFailedTransfer(t, 2, 100, 200, 100)

	tok := &model.APIToken{ID: 18, UserID: 999}
	tok.SetServerIDs([]uint64{1})

	respOutside, statusOutside := callRetryServerTransferWithAdminPAT(t, outsideID, 999, tok)
	assert.Equal(t, http.StatusOK, statusOutside)
	assert.False(t, respOutside.Success,
		"admin PAT scoped to {1} must NOT retry transfer of server 2 (admin early-return is no longer a bypass)")
	assert.Contains(t, respOutside.Error, "permission denied")

	var count int64
	assert.NoError(t, singleton.DB.Model(&model.ServerTransfer{}).
		Where("status = ?", model.ServerTransferStatusPending).
		Count(&count).Error)
	assert.Equal(t, int64(0), count,
		"rejected retry must not create a Pending row")

	respInside, statusInside := callRetryServerTransferWithAdminPAT(t, insideID, 999, tok)
	assert.Equal(t, http.StatusOK, statusInside)
	assert.True(t, respInside.Success,
		"admin PAT scoped to {1} must still be able to retry transfer of server 1: %s",
		respInside.Error)
}
