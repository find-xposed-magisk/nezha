package model

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// stubPATAccessor 是只在测试里用的最小 APITokenAccessor，仅按 ids
// 字面包含判断。够用就行，不引入 *APIToken 在 model 包里转译 CSV。
type stubPATAccessor struct {
	ids []uint64
}

func (s *stubPATAccessor) CanAccessServer(id uint64) bool {
	for _, x := range s.ids {
		if x == id {
			return true
		}
	}
	return false
}

// ServerIDs 暴露白名单，使 DenyListSafeForLimitedPAT 能区分「unscoped PAT」
// 与「server-limited PAT」；缺这个方法时所有 stub 都会被当作不受限放行。
func (s *stubPATAccessor) ServerIDs() []uint64 {
	return s.ids
}

// 钉死「server-limited PAT 不能通过 cover-all + 空 Servers 越过白名单」。
// 老实现在 len(c.Servers)==0 时直接放行，但 CronCoverAll + 空 Servers 在
// CronTrigger 里会 fan out 到 owner 的所有 server（包含白名单外的）。
// HasPermission 是 cron 列表/手动触发/删除路径上唯一的 PAT 收口，
// 因此这里必须拒绝。
func TestCronHasPermission_DeniesCoverAllEmptyServersForLimitedPAT(t *testing.T) {
	cron := &Cron{
		Common:  Common{ID: 9, UserID: 100},
		Cover:   CronCoverAll,
		Servers: nil,
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})

	if cron.HasPermission(ctx) {
		t.Fatal("server-limited PAT must not be allowed to operate on a CronCoverAll cron with empty Servers")
	}
}

// CoverIgnoreAll + 空 Servers 在 CronTrigger 里是 “allow-list of zero”，
// 不会 fan out。允许 PAT 继续看到/触发是无害的，但 HasPermission 的
// 老语义在这一组合下仍是 return true，所以这条测试是「保持现状」的金线，
// 防止未来收紧时把这一无害情况也误拒。
func TestCronHasPermission_AllowsCoverIgnoreAllEmptyServersForLimitedPAT(t *testing.T) {
	cron := &Cron{
		Common:  Common{ID: 10, UserID: 100},
		Cover:   CronCoverIgnoreAll,
		Servers: nil,
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
	ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})

	if !cron.HasPermission(ctx) {
		t.Fatal("CronCoverIgnoreAll + empty Servers is a no-op cron; server-limited PAT must remain allowed")
	}
}

// 现有 non-empty Servers 路径必须保持不变：白名单内允许、白名单外拒绝。
// 这条用例钉死「修复 cover-all 路径时不能误改这条已有的金线」。
func TestCronHasPermission_KeepsExistingNonEmptyServersSemantics(t *testing.T) {
	t.Run("whitelisted", func(t *testing.T) {
		cron := &Cron{
			Common:  Common{ID: 11, UserID: 100},
			Cover:   CronCoverIgnoreAll,
			Servers: []uint64{1},
		}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
		ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})
		if !cron.HasPermission(ctx) {
			t.Fatal("cron bound to whitelisted server 1 must remain accessible to PAT [1]")
		}
	})

	t.Run("outside whitelist", func(t *testing.T) {
		cron := &Cron{
			Common:  Common{ID: 12, UserID: 100},
			Cover:   CronCoverIgnoreAll,
			Servers: []uint64{2},
		}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 100}, Role: RoleMember})
		ctx.Set(CtxKeyAPIToken, &stubPATAccessor{ids: []uint64{1}})
		if cron.HasPermission(ctx) {
			t.Fatal("cron bound to non-whitelisted server 2 must be rejected for PAT [1]")
		}
	})
}
