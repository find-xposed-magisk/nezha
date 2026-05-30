package model

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// C1 regression: an admin-owned CronCoverAll fans out to every server in the
// system at runtime (CronTrigger gates on userIsAdmin(cr.UserID)). A
// server-limited PAT created by that admin must therefore only pass
// HasPermission when its deny-list covers EVERY server outside its whitelist
// system-wide — not just the admin's own servers, which is the visibly
// degenerate set that OwnerServerIDsLookup returns today.
//
// Without this regression, an admin with a PAT scoped to server X can create
// a CronCoverAll cron with deny-list = [X] and the dashboard will cheerfully
// dispatch the command to every OTHER user's server.
func TestCronHasPermission_AdminOwnerCoverAllDeniesUntilDenyListCoversAllOtherServers(t *testing.T) {
	saved := OwnerServerIDsLookup
	savedAdmin := OwnerIsAdminLookup
	savedAll := AllServerIDsLookup
	t.Cleanup(func() {
		OwnerServerIDsLookup = saved
		OwnerIsAdminLookup = savedAdmin
		AllServerIDsLookup = savedAll
	})

	// Admin (uid=1) owns only server 1. Member (uid=200) owns server 2.
	OwnerServerIDsLookup = func(ownerUID uint64) []uint64 {
		switch ownerUID {
		case 1:
			return []uint64{1}
		case 200:
			return []uint64{2}
		}
		return nil
	}
	OwnerIsAdminLookup = func(uid uint64) bool { return uid == 1 }
	AllServerIDsLookup = func() []uint64 { return []uint64{1, 2} }

	// Limited PAT (admin's) — scoped to server 1 only.
	pat := &stubPATAccessor{ids: []uint64{1}}

	t.Run("deny_list_missing_other_owner_server_must_reject", func(t *testing.T) {
		cron := &Cron{
			Common:  Common{ID: 9, UserID: 1}, // admin-owned
			Cover:   CronCoverAll,
			Servers: []uint64{1}, // deny self, but NOT server 2 (member's)
		}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 1}, Role: RoleAdmin})
		ctx.Set(CtxKeyAPIToken, pat)

		if cron.HasPermission(ctx) {
			t.Fatal("admin-owned CronCoverAll fans out to ALL servers at runtime; " +
				"deny-list missing server 2 must reject a PAT scoped to [1] — otherwise " +
				"the cron will execute on a foreign user's server")
		}
	})

	t.Run("deny_list_covers_all_other_servers_passes", func(t *testing.T) {
		cron := &Cron{
			Common:  Common{ID: 10, UserID: 1},
			Cover:   CronCoverAll,
			Servers: []uint64{2}, // deny the only server outside PAT whitelist
		}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 1}, Role: RoleAdmin})
		ctx.Set(CtxKeyAPIToken, pat)

		if !cron.HasPermission(ctx) {
			t.Fatal("deny-list covering every non-whitelisted server must pass: fan-out " +
				"is now contained inside the PAT whitelist")
		}
	})
}

// Companion: member-owned crons must NOT use the system-wide fan-out set.
// Runtime CronTrigger only ships to servers whose UserID matches the
// member-owner; HasPermission must mirror that to avoid false rejects on
// completely legitimate configs.
func TestCronHasPermission_MemberOwnerCoverAllStillUsesOwnerSet(t *testing.T) {
	saved := OwnerServerIDsLookup
	savedAdmin := OwnerIsAdminLookup
	savedAll := AllServerIDsLookup
	t.Cleanup(func() {
		OwnerServerIDsLookup = saved
		OwnerIsAdminLookup = savedAdmin
		AllServerIDsLookup = savedAll
	})

	OwnerServerIDsLookup = func(ownerUID uint64) []uint64 {
		if ownerUID == 100 {
			return []uint64{1}
		}
		return nil
	}
	OwnerIsAdminLookup = func(uid uint64) bool { return false }
	AllServerIDsLookup = func() []uint64 { return []uint64{1, 2, 3} }

	cron := &Cron{
		Common:  Common{ID: 9, UserID: 100},
		Cover:   CronCoverAll,
		Servers: []uint64{}, // empty deny-list: fan-out = owner-set = [1]
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	// PAT whitelist = [1] — covers everything the member owner can fan out to.
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})

	if !cron.HasPermission(ctx) {
		t.Fatal("member-owned CronCoverAll fans out to owner servers only; PAT [1] covers them all")
	}
}
