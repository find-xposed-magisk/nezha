package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/nezhahq/nezha/model"
)

func newServiceVisibilityCtx(viewer *model.User) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if viewer != nil {
		c.Set(model.CtxKeyAuthorizedUser, viewer)
	}
	return c
}

func TestUserCanViewServiceVisibleServiceIsPublic(t *testing.T) {
	visible := &model.Service{Common: model.Common{ID: 1, UserID: 100}, EnableShowInService: true}
	assert.True(t, userCanViewService(newServiceVisibilityCtx(nil), visible), "guest must see EnableShowInService=true regardless of owner")
}

func TestUserCanViewServiceHiddenServiceRejectsGuest(t *testing.T) {
	hidden := &model.Service{Common: model.Common{ID: 1, UserID: 100}, EnableShowInService: false}
	assert.False(t, userCanViewService(newServiceVisibilityCtx(nil), hidden), "guest must NOT see hidden service via per-server / per-id sideband endpoints")
}

func TestUserCanViewServiceHiddenServiceRejectsForeignMember(t *testing.T) {
	hidden := &model.Service{Common: model.Common{ID: 1, UserID: 100}, EnableShowInService: false}
	foreign := &model.User{Common: model.Common{ID: 200}, Role: model.RoleMember}
	assert.False(t, userCanViewService(newServiceVisibilityCtx(foreign), hidden), "foreign member must NOT see another user's hidden service")
}

func TestUserCanViewServiceHiddenServiceAllowsOwner(t *testing.T) {
	hidden := &model.Service{Common: model.Common{ID: 1, UserID: 100}, EnableShowInService: false}
	owner := &model.User{Common: model.Common{ID: 100}, Role: model.RoleMember}
	assert.True(t, userCanViewService(newServiceVisibilityCtx(owner), hidden), "owner must still see their own hidden service")
}

func TestUserCanViewServiceHiddenServiceAllowsAdmin(t *testing.T) {
	hidden := &model.Service{Common: model.Common{ID: 1, UserID: 100}, EnableShowInService: false}
	admin := &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}
	assert.True(t, userCanViewService(newServiceVisibilityCtx(admin), hidden), "admin must be able to see any hidden service")
}

// 钉死 admin 自己签发的 server_ids 受限 PAT 不能借助 admin 身份在
// service 可见性入口绕过白名单：与 userCanViewServer 的 PAT-first 收口
// 保持对称，避免 hidden service 通过 admin 早返回泄漏给受限 PAT。
func TestUserCanViewServiceLimitedPATShouldDenyAdminWhenOutsideWhitelist(t *testing.T) {
	hidden := &model.Service{
		Common:      model.Common{ID: 1, UserID: 100},
		Cover:       model.ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{2: true},
	}
	admin := &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}
	tok := &model.APIToken{ID: 7, UserID: 1}
	tok.SetServerIDs([]uint64{1})

	ctx := newServiceVisibilityCtx(admin)
	ctx.Set(model.CtxKeyAPIToken, tok)

	assert.False(t, userCanViewService(ctx, hidden),
		"admin caller using a server_ids=[1] PAT must NOT see a CoverIgnoreAll service whose only target is the non-whitelisted server 2")
}

func TestUserCanViewServiceLimitedPATAllowsAdminInsideWhitelist(t *testing.T) {
	visible := &model.Service{
		Common:      model.Common{ID: 2, UserID: 100},
		Cover:       model.ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{1: true},
	}
	admin := &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}
	tok := &model.APIToken{ID: 7, UserID: 1}
	tok.SetServerIDs([]uint64{1})

	ctx := newServiceVisibilityCtx(admin)
	ctx.Set(model.CtxKeyAPIToken, tok)

	assert.True(t, userCanViewService(ctx, visible),
		"admin caller using a server_ids=[1] PAT must still see a CoverIgnoreAll service bound to whitelisted server 1")
}
