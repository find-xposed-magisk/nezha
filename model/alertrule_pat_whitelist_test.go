package model

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// H4 regression: AlertRule had no AlertRule.HasPermission override, so
// limited PATs could create / list / update rules that fan out to every
// owner server. A RuleCoverAll + empty Ignore rule monitors every server
// the owner can reach (admin owner: every server in the system), which is
// exactly the cover-fanout the PAT whitelist is supposed to contain.
func TestAlertRuleHasPermission_DeniesRuleCoverAllEmptyIgnoreForLimitedPAT(t *testing.T) {
	saved := OwnerServerIDsLookup
	savedAdmin := OwnerIsAdminLookup
	savedAll := AllServerIDsLookup
	t.Cleanup(func() {
		OwnerServerIDsLookup = saved
		OwnerIsAdminLookup = savedAdmin
		AllServerIDsLookup = savedAll
	})
	OwnerServerIDsLookup = func(uid uint64) []uint64 {
		if uid == 100 {
			return []uint64{1, 2}
		}
		return nil
	}
	OwnerIsAdminLookup = func(uid uint64) bool { return false }
	AllServerIDsLookup = func() []uint64 { return []uint64{1, 2, 3} }

	rule := &AlertRule{
		Common: Common{ID: 9, UserID: 100},
		Rules: []*Rule{{
			Type:   "cpu",
			Cover:  RuleCoverAll,
			Ignore: nil,
		}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}}) // doesn't cover 2

	if rule.HasPermission(ctx) {
		t.Fatal("server-limited PAT must not be allowed to operate on RuleCoverAll with empty Ignore — runtime fans out to owner server 2")
	}
}

func TestAlertRuleHasPermission_AllowsRuleCoverIgnoreAllEmptyIgnore(t *testing.T) {
	saved := OwnerServerIDsLookup
	t.Cleanup(func() { OwnerServerIDsLookup = saved })
	OwnerServerIDsLookup = func(uid uint64) []uint64 { return []uint64{1, 2} }

	rule := &AlertRule{
		Common: Common{ID: 10, UserID: 100},
		Rules: []*Rule{{
			Type:   "cpu",
			Cover:  RuleCoverIgnoreAll,
			Ignore: nil, // allow-list of zero ⇒ no-op
		}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})

	if !rule.HasPermission(ctx) {
		t.Fatal("RuleCoverIgnoreAll + empty Ignore is a no-op rule; PAT must remain allowed")
	}
}

func TestAlertRuleHasPermission_DeniesUnknownCover(t *testing.T) {
	saved := OwnerServerIDsLookup
	t.Cleanup(func() { OwnerServerIDsLookup = saved })
	OwnerServerIDsLookup = func(uid uint64) []uint64 { return []uint64{1} }

	rule := &AlertRule{
		Common: Common{ID: 11, UserID: 100},
		Rules: []*Rule{{
			Type:  "cpu",
			Cover: 99, // unknown ⇒ runtime Snapshot falls through to "monitor everything"
		}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})

	if rule.HasPermission(ctx) {
		t.Fatal("unknown Rule.Cover must fail-closed for limited PAT — Snapshot does not gate on it")
	}
}

// Regression: AlertRule.HasPermission built the deny-list from every key in
// Rule.Ignore regardless of its bool value, but Rule.Snapshot only skips a
// server when Ignore[id] == true. A limited PAT whitelisted to {1} could
// submit RuleCoverAll with Ignore{2: false}; the permission check treated 2 as
// denied (safe) while the runtime still monitored server 2.
func TestAlertRuleHasPermission_DeniesRuleCoverAllIgnoreFalseForLimitedPAT(t *testing.T) {
	saved := OwnerServerIDsLookup
	savedAdmin := OwnerIsAdminLookup
	savedAll := AllServerIDsLookup
	t.Cleanup(func() {
		OwnerServerIDsLookup = saved
		OwnerIsAdminLookup = savedAdmin
		AllServerIDsLookup = savedAll
	})
	OwnerServerIDsLookup = func(uid uint64) []uint64 {
		if uid == 100 {
			return []uint64{1, 2}
		}
		return nil
	}
	OwnerIsAdminLookup = func(uid uint64) bool { return false }
	AllServerIDsLookup = func() []uint64 { return []uint64{1, 2, 3} }

	rule := &AlertRule{
		Common: Common{ID: 13, UserID: 100},
		Rules: []*Rule{{
			Type:   "cpu",
			Cover:  RuleCoverAll,
			Ignore: map[uint64]bool{2: false}, // key present but NOT actually ignored at runtime
		}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}}) // doesn't cover 2

	if rule.HasPermission(ctx) {
		t.Fatal("Ignore{2:false} does NOT exclude server 2 at runtime; limited PAT must be denied")
	}
}

// A genuine deny entry (value true) for every out-of-whitelist server keeps the
// rule contained and must remain allowed.
func TestAlertRuleHasPermission_AllowsRuleCoverAllIgnoreTrueCoversWhitelistGap(t *testing.T) {
	saved := OwnerServerIDsLookup
	savedAdmin := OwnerIsAdminLookup
	savedAll := AllServerIDsLookup
	t.Cleanup(func() {
		OwnerServerIDsLookup = saved
		OwnerIsAdminLookup = savedAdmin
		AllServerIDsLookup = savedAll
	})
	OwnerServerIDsLookup = func(uid uint64) []uint64 { return []uint64{1, 2} }
	OwnerIsAdminLookup = func(uid uint64) bool { return false }
	AllServerIDsLookup = func() []uint64 { return []uint64{1, 2} }

	rule := &AlertRule{
		Common: Common{ID: 14, UserID: 100},
		Rules: []*Rule{{
			Type:   "cpu",
			Cover:  RuleCoverAll,
			Ignore: map[uint64]bool{2: true}, // server 2 genuinely excluded
		}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})

	if !rule.HasPermission(ctx) {
		t.Fatal("Ignore{2:true} excludes the only out-of-whitelist server; PAT must be allowed")
	}
}

// Regression: the RuleCoverIgnoreAll branch checked CanAccessServer for every
// key in Rule.Ignore, but Rule.Snapshot only monitors a server when
// Ignore[id] == true. A limited PAT whitelisted to {1} submitting
// RuleCoverIgnoreAll with Ignore{2: false} (server 2 is NOT monitored at
// runtime) was wrongly denied because the foreign key 2 failed the whitelist.
func TestAlertRuleHasPermission_AllowsRuleCoverIgnoreAllIgnoreFalseForLimitedPAT(t *testing.T) {
	saved := OwnerServerIDsLookup
	t.Cleanup(func() { OwnerServerIDsLookup = saved })
	OwnerServerIDsLookup = func(uid uint64) []uint64 { return []uint64{1, 2} }

	rule := &AlertRule{
		Common: Common{ID: 15, UserID: 100},
		Rules: []*Rule{{
			Type:   "cpu",
			Cover:  RuleCoverIgnoreAll,
			Ignore: map[uint64]bool{2: false}, // key present but server 2 is NOT monitored
		}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}}) // doesn't cover 2

	if !rule.HasPermission(ctx) {
		t.Fatal("Ignore{2:false} does NOT monitor server 2 at runtime; limited PAT must remain allowed")
	}
}

// The genuine allow entry (value true) for an out-of-whitelist server is the
// case that must still be denied.
func TestAlertRuleHasPermission_DeniesRuleCoverIgnoreAllIgnoreTrueForLimitedPAT(t *testing.T) {
	saved := OwnerServerIDsLookup
	t.Cleanup(func() { OwnerServerIDsLookup = saved })
	OwnerServerIDsLookup = func(uid uint64) []uint64 { return []uint64{1, 2} }

	rule := &AlertRule{
		Common: Common{ID: 16, UserID: 100},
		Rules: []*Rule{{
			Type:   "cpu",
			Cover:  RuleCoverIgnoreAll,
			Ignore: map[uint64]bool{2: true}, // server 2 IS monitored at runtime
		}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}}) // doesn't cover 2

	if rule.HasPermission(ctx) {
		t.Fatal("Ignore{2:true} monitors server 2; server-limited PAT must be denied")
	}
}

func TestAlertRuleHasPermission_NoPATPassesViaCommonHasPermission(t *testing.T) {
	rule := &AlertRule{
		Common: Common{ID: 12, UserID: 100},
		Rules:  []*Rule{{Type: "cpu", Cover: RuleCoverAll}},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})

	if !rule.HasPermission(ctx) {
		t.Fatal("owner without PAT must keep the existing owner/admin pass")
	}
}
