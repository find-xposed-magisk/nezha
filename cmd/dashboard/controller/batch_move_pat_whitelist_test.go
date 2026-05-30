package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func callBatchMoveWithPAT(t *testing.T, callerID uint64, role model.Role, tok *model.APIToken, body string) ([]model.BatchMoveServerResult, bool, string) {
	t.Helper()
	r := gin.New()
	r.Use(newPATCtxSetter(callerID, role, tok))
	r.POST("/batch-move/server", commonHandler(batchMoveServer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/batch-move/server", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	var resp struct {
		Success bool                           `json:"success"`
		Error   string                         `json:"error"`
		Data    []model.BatchMoveServerResult  `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data, resp.Success, resp.Error
}

func TestBatchMoveServer_AdminPATScopeNarrowsServerIDs(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()
	seedServer(t, 1, 999)
	seedServer(t, 2, 999)

	tok := &model.APIToken{ID: 18, UserID: 999}
	tok.SetServerIDs([]uint64{1})

	data, ok, errStr := callBatchMoveWithPAT(t, 999, model.RoleAdmin, tok,
		`{"ids":[1,2],"to_user":200}`)
	assert.True(t, ok, "batch-move call must succeed at the request layer: %s", errStr)
	require.Len(t, data, 2)

	resultByID := map[uint64]model.BatchMoveServerResult{}
	for _, r := range data {
		resultByID[r.ServerID] = r
	}

	assert.NotEqual(t, model.BatchMoveServerResultPending, resultByID[2].Status,
		"admin PAT scoped to {1} MUST NOT be able to move server 2; got status=%q error=%q",
		resultByID[2].Status, resultByID[2].Error)

	var pending int64
	require.NoError(t, singleton.DB.Model(&model.ServerTransfer{}).
		Where("server_id = ? AND status = ?", 2, model.ServerTransferStatusPending).
		Count(&pending).Error)
	assert.Equal(t, int64(0), pending,
		"rejected batch-move of server 2 must not create a Pending row")

	assert.Equal(t, model.BatchMoveServerResultPending, resultByID[1].Status,
		"admin PAT scoped to {1} must still be able to move server 1; got status=%q error=%q",
		resultByID[1].Status, resultByID[1].Error)
}
