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

// cancelServerTransfer 的核心租户安全语义：
//   - admin 可以取消任意 transfer 行
//   - member 只能取消自己作为 FromUserID 的 transfer
//   - 行不存在 vs 行存在但调用者不是 FromUserID 必须返回**相同的** "permission denied"，
//     避免通过响应差异枚举 transfer ID 是否存在
//
// 该 handler 已有保护（transfer.go:73-80），但此前没有任何测试盯住它。

func seedPendingTransfer(t *testing.T, serverID, fromUID, toUID, initUID uint64) uint64 {
	t.Helper()
	tr := &model.ServerTransfer{
		ServerID:    serverID,
		FromUserID:  fromUID,
		ToUserID:    toUID,
		InitiatorID: initUID,
		Status:      model.ServerTransferStatusPending,
	}
	assert.NoError(t, singleton.DB.Create(tr).Error)
	singleton.ServerTransferShared.Register(tr)
	return tr.ID
}

func callCancelServerTransfer(t *testing.T, transferID, callerID uint64, role model.Role) (commonResponseShape, int) {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, callerID, role)
		c.Next()
	})
	r.POST("/transfer/:id/cancel", commonHandler(cancelServerTransfer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/transfer/"+strconv.FormatUint(transferID, 10)+"/cancel",
		bytes.NewReader(nil))
	r.ServeHTTP(w, req)

	var resp commonResponseShape
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp, w.Code
}

func TestCancelServerTransfer_MemberCancelsOwnTransfer(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()
	seedServer(t, 1, 100)
	id := seedPendingTransfer(t, 1, 100, 200, 100)

	resp, status := callCancelServerTransfer(t, id, 100, model.RoleMember)
	assert.Equal(t, http.StatusOK, status)
	assert.True(t, resp.Success, "FromUserID member must be able to cancel own transfer: %s", resp.Error)
}

func TestCancelServerTransfer_MemberCannotCancelOthers(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()
	seedServer(t, 1, 100)
	id := seedPendingTransfer(t, 1, 100, 200, 100)

	resp, status := callCancelServerTransfer(t, id, 200, model.RoleMember)
	assert.Equal(t, http.StatusOK, status)
	assert.False(t, resp.Success, "ToUserID member must NOT be able to cancel another user's transfer")
	assert.Contains(t, resp.Error, "permission denied")
}

func TestCancelServerTransfer_MemberCannotEnumerateNonexistentIDs(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()

	resp, status := callCancelServerTransfer(t, 99999, 100, model.RoleMember)
	assert.Equal(t, http.StatusOK, status)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "permission denied",
		"nonexistent transfer must return the SAME error as 'not your transfer', "+
			"so an attacker can't probe which transfer IDs exist")
}

func TestCancelServerTransfer_AdminCancelsAny(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()
	seedServer(t, 1, 100)
	id := seedPendingTransfer(t, 1, 100, 200, 100)

	resp, status := callCancelServerTransfer(t, id, 999, model.RoleAdmin)
	assert.Equal(t, http.StatusOK, status)
	assert.True(t, resp.Success, "admin must be able to cancel any transfer: %s", resp.Error)
}
