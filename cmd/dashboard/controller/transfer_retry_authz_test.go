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
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/singleton"
)

// retryServerTransfer previously gated on prev.HasPermission(c), which honours
// the historical transfer row (FromUserID, ToUserID, InitiatorID). That lets
// any of those original parties re-initiate a transfer of the server long
// after ownership has moved on. Concretely: a stale "alice -> bob" Failed row
// stays visible to alice forever — even after she's transferred the server
// off to charlie — and the original endpoint would happily move it from
// charlie to bob without ever consulting the current owner.
//
// Authorization for an action that mutates the live server must use the
// live server, not a historical audit row.
func TestRetryServerTransferRejectsCallerWhoNoLongerOwnsServer(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()

	// Seed: server originally owned by user 100 (alice). Failed transfer to
	// 200 (bob) is recorded but server ownership has since moved to 300
	// (charlie) — e.g. alice transferred elsewhere afterwards. Alice is no
	// longer the owner, so retrying the stale row would be an unauthorized
	// grab.
	seedServer(t, 1, 300)
	staleID := seedFailedTransfer(t, 1, 100 /*from*/, 200 /*to*/, 100 /*initiator*/)

	resp, status := callRetryServerTransfer(t, staleID, 100, model.RoleMember)

	assert.Equal(t, http.StatusOK, status)
	assert.False(t, resp.Success, "alice no longer owns server 1; retry must be rejected")
	assert.Contains(t, resp.Error, "permission denied")

	var s model.Server
	assert.NoError(t, singleton.DB.First(&s, 1).Error)
	assert.Equal(t, uint64(300), s.UserID, "rejected retry must not flip ownership")

	var count int64
	assert.NoError(t, singleton.DB.Model(&model.ServerTransfer{}).Where("status = ?", model.ServerTransferStatusPending).Count(&count).Error)
	assert.Equal(t, int64(0), count, "rejected retry must not create a Pending row")
}

// The historical ToUserID must also not be able to grab the server back via
// the stale row. Same root cause; this is the explicit assertion that the
// fix covers the To side, not just the From side.
func TestRetryServerTransferRejectsHistoricalTargetWhoNeverOwnedServer(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()

	seedServer(t, 1, 300)
	staleID := seedFailedTransfer(t, 1, 100, 200, 100)

	resp, status := callRetryServerTransfer(t, staleID, 200, model.RoleMember)

	assert.Equal(t, http.StatusOK, status)
	assert.False(t, resp.Success, "bob was the failed transfer's target; he never owned server 1")
	assert.Contains(t, resp.Error, "permission denied")
}

// Members never retry: batchMoveServer's "ToUser == self" policy means a
// member can only RECEIVE a server, not give one away. Retry of a failed
// "alice -> bob" by alice (member, current owner) is exactly the give-away
// case batch-move would refuse. Retry is admin-only.
func TestRetryServerTransferRejectsCurrentOwnerWhoIsMember(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()

	seedServer(t, 1, 100)
	failedID := seedFailedTransfer(t, 1, 100, 200, 100)

	resp, status := callRetryServerTransfer(t, failedID, 100, model.RoleMember)

	assert.Equal(t, http.StatusOK, status)
	assert.False(t, resp.Success, "member retry is forbidden — the give-away semantics bypass batchMoveServer's ToUser==self policy")
	assert.Contains(t, resp.Error, "permission denied")
}

// batchMoveServer enforces "non-admin caller may only move a server TO
// themselves" (controller/server.go: ToUser != getUid(c) returns permission
// denied). retryServerTransfer historically only checked the live owner
// and not the transfer's ToUserID, which let the current owner re-push the
// server to ANY historical ToUserID — bypassing the batch-move policy.
//
// Concretely: alice (member) currently owns server 1; she finds a Failed
// transfer whose ToUserID is bob and retries it. The server lands on bob
// even though batch-move would have refused "alice -> bob" from her.
func TestRetryServerTransferRejectsNonAdminPushingToForeignToUserID(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()

	seedServer(t, 1, 100)
	staleID := seedFailedTransfer(t, 1, 100 /*from*/, 200 /*to*/, 100 /*initiator*/)

	resp, status := callRetryServerTransfer(t, staleID, 100, model.RoleMember)

	assert.Equal(t, http.StatusOK, status)
	assert.False(t, resp.Success, "non-admin owner cannot push their server to a historical foreign ToUserID — that would bypass batchMoveServer's ToUser==self policy")
	assert.Contains(t, resp.Error, "permission denied")

	var s model.Server
	assert.NoError(t, singleton.DB.First(&s, 1).Error)
	assert.Equal(t, uint64(100), s.UserID, "rejected retry must not flip ownership")

	var count int64
	assert.NoError(t, singleton.DB.Model(&model.ServerTransfer{}).Where("status = ?", model.ServerTransferStatusPending).Count(&count).Error)
	assert.Equal(t, int64(0), count, "rejected retry must not create a Pending row")
}

// Admins must always be able to retry — they are the last-resort recovery
// path when an operator-cancelled transfer needs to be re-pushed.
func TestRetryServerTransferAllowsAdmin(t *testing.T) {
	cleanup := setupRetryServerTransferFixture(t)
	defer cleanup()

	seedServer(t, 1, 300)
	failedID := seedFailedTransfer(t, 1, 100, 200, 100)

	resp, status := callRetryServerTransfer(t, failedID, 999, model.RoleAdmin)

	assert.Equal(t, http.StatusOK, status)
	assert.True(t, resp.Success, "admin must be able to retry any transfer: error=%s", resp.Error)
}

func setupRetryServerTransferFixture(t *testing.T) func() {
	t.Helper()
	if singleton.Localizer == nil {
		singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	}
	originalDB := singleton.DB
	originalShared := singleton.ServerShared
	originalTransferShared := singleton.ServerTransferShared
	originalUserMap := singleton.UserInfoMap

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&model.Server{}, &model.ServerTransfer{}))
	singleton.DB = db
	singleton.ServerShared = singleton.NewServerClass()
	singleton.UserInfoMap = map[uint64]model.UserInfo{
		100: {Role: model.RoleMember, AgentSecret: "alice-secret"},
		200: {Role: model.RoleMember, AgentSecret: "bob-secret"},
		300: {Role: model.RoleMember, AgentSecret: "charlie-secret"},
	}
	singleton.ServerTransferShared = singleton.NewServerTransferClass()

	return func() {
		if singleton.ServerTransferShared != nil {
			singleton.ServerTransferShared.Stop()
		}
		singleton.DB = originalDB
		singleton.ServerShared = originalShared
		singleton.ServerTransferShared = originalTransferShared
		singleton.UserInfoMap = originalUserMap
	}
}

func seedServer(t *testing.T, id, ownerID uint64) {
	t.Helper()
	s := &model.Server{
		Common: model.Common{ID: id, UserID: ownerID},
		UUID:   "uuid-" + strconv.FormatUint(id, 10),
		Name:   "seeded",
	}
	assert.NoError(t, singleton.DB.Create(s).Error)
	model.InitServer(s)
	singleton.ServerShared.Update(s, s.UUID)
}

func seedFailedTransfer(t *testing.T, serverID, fromUserID, toUserID, initiatorID uint64) uint64 {
	t.Helper()
	tr := &model.ServerTransfer{
		ServerID:    serverID,
		FromUserID:  fromUserID,
		ToUserID:    toUserID,
		InitiatorID: initiatorID,
		Status:      model.ServerTransferStatusFailed,
		LastError:   "seeded",
	}
	assert.NoError(t, singleton.DB.Create(tr).Error)
	return tr.ID
}

func callRetryServerTransfer(t *testing.T, transferID, callerID uint64, role model.Role) (commonResponseShape, int) {
	t.Helper()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, callerID, role)
		c.Next()
	})
	r.POST("/transfer/:id/retry", commonHandler(retryServerTransfer))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/transfer/"+strconv.FormatUint(transferID, 10)+"/retry", bytes.NewReader(nil))
	r.ServeHTTP(w, req)

	var resp commonResponseShape
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp, w.Code
}

type commonResponseShape struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}
