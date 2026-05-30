package model

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// Regression: NAT had no HasPermission override, so it fell back to
// Common.HasPermission (owner/admin only). listHandler/CheckPermission gate
// on NAT.HasPermission, meaning a server-limited PAT could list/update/delete
// NAT records bound to off-whitelist servers of the same owner. NAT now
// applies CanAccessServer(NAT.ServerID) like Server/Service/Cron.
func TestNATHasPermission_DeniesOffWhitelistServerForLimitedPAT(t *testing.T) {
	nat := &NAT{Common: Common{ID: 1, UserID: 100}, ServerID: 2}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}}) // whitelist excludes server 2

	if nat.HasPermission(ctx) {
		t.Fatal("server-limited PAT must not reach a NAT bound to an off-whitelist server")
	}
}

func TestNATHasPermission_AllowsWhitelistedServerForLimitedPAT(t *testing.T) {
	nat := &NAT{Common: Common{ID: 1, UserID: 100}, ServerID: 1}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})

	if !nat.HasPermission(ctx) {
		t.Fatal("PAT whitelisted to the NAT's server must be allowed")
	}
}

func TestNATHasPermission_NoPATPassesViaCommonHasPermission(t *testing.T) {
	nat := &NAT{Common: Common{ID: 1, UserID: 100}, ServerID: 2}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})

	if !nat.HasPermission(ctx) {
		t.Fatal("owner without PAT must keep the existing owner/admin pass")
	}
}

func TestNATHasPermission_DeniesNonOwner(t *testing.T) {
	nat := &NAT{Common: Common{ID: 1, UserID: 100}, ServerID: 1}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 200}, Role: RoleMember})

	if nat.HasPermission(ctx) {
		t.Fatal("a different non-admin user must not reach another owner's NAT")
	}
}
