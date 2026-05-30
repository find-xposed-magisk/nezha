package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/singleton"
)

func ensureLocalizerForServiceTest(t *testing.T) {
	t.Helper()
	if singleton.Localizer == nil {
		singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	}
}

// M13 regression: checkServiceSkipServerPermission must treat SkipServers
// as a typed map[uint64]bool where only `true` entries actually skip
// at runtime (DispatchTask only consults true keys). Entries with value
// false carry no dispatch meaning, so requiring HasPermission on them
// rejects perfectly legitimate updates from members whose PAT does not
// own the no-op `{2: false}` server.
func TestCheckServiceSkipServerPermission_IgnoresFalseEntries(t *testing.T) {
	ensureLocalizerForServiceTest(t)
	saved := singleton.ServerShared
	t.Cleanup(func() { singleton.ServerShared = saved })
	sc := singleton.NewEmptyServerClassForTest()
	sc.InsertForTest(&model.Server{Common: model.Common{ID: 1, UserID: 100}})
	sc.InsertForTest(&model.Server{Common: model.Common{ID: 2, UserID: 999}})
	singleton.ServerShared = sc

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 100}, Role: model.RoleMember})

	skip := map[uint64]bool{
		1: true,  // member owns it — legal allow-list entry
		2: false, // no-op entry; member doesn't own server 2 but it's not actually skipped
	}
	if err := checkServiceSkipServerPermission(ctx, model.ServiceCoverIgnoreAll, skip, 100); err != nil {
		t.Fatalf("`{2: false}` must NOT trigger permission denied — it has no runtime dispatch effect, got %v", err)
	}
}

func TestCheckServiceSkipServerPermission_RejectsForeignTrueEntries(t *testing.T) {
	ensureLocalizerForServiceTest(t)
	saved := singleton.ServerShared
	t.Cleanup(func() { singleton.ServerShared = saved })
	sc := singleton.NewEmptyServerClassForTest()
	sc.InsertForTest(&model.Server{Common: model.Common{ID: 1, UserID: 100}})
	sc.InsertForTest(&model.Server{Common: model.Common{ID: 2, UserID: 999}})
	singleton.ServerShared = sc

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 100}, Role: model.RoleMember})

	skip := map[uint64]bool{
		2: true, // member doesn't own server 2 — true entry IS the allow-list, must reject
	}
	if err := checkServiceSkipServerPermission(ctx, model.ServiceCoverIgnoreAll, skip, 100); err == nil {
		t.Fatal("true entry pointing at foreign-owned server must still be rejected — pre-existing safety invariant")
	}
}
