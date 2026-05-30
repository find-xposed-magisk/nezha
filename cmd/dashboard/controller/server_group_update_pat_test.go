package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// H1 regression: updateServerGroup must reject a server-limited PAT whose
// whitelist does not cover the group's CURRENT membership. Today the
// handler only checks the incoming sg.Servers list and then unconditionally
// `DELETE FROM server_group_server WHERE server_group_id = ?`, so a PAT
// scoped to [X] can remove server Y (owned by another tenant or just
// outside the whitelist) from a group it shares with X.
func TestPatHasGroupMembershipAccess_DeniesGroupContainingOutsideServer(t *testing.T) {
	db := newTestDB(t)
	swap := swapSingletonDB(t, db)
	defer swap()

	if err := db.Create(&model.ServerGroupServer{
		Common:        model.Common{ID: 1, UserID: 1},
		ServerGroupId: 42,
		ServerId:      9, // outside whitelist
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ServerGroupServer{
		Common:        model.Common{ID: 2, UserID: 1},
		ServerGroupId: 42,
		ServerId:      1, // inside whitelist
	}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin})
	ctx.Set(model.CtxKeyAPIToken, &model.APIToken{ServersCSV: "1"})

	if patGroupMembershipAccessAllowed(ctx, 42) {
		t.Fatal("PAT scoped to [1] must NOT be allowed to mutate a group whose current members include server 9; " +
			"transactional DELETE+INSERT would drop server 9 from the group")
	}
}

func TestPatHasGroupMembershipAccess_AllowsGroupFullyInsideWhitelist(t *testing.T) {
	db := newTestDB(t)
	swap := swapSingletonDB(t, db)
	defer swap()

	if err := db.Create(&model.ServerGroupServer{
		Common:        model.Common{ID: 1, UserID: 1},
		ServerGroupId: 7,
		ServerId:      1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin})
	ctx.Set(model.CtxKeyAPIToken, &model.APIToken{ServersCSV: "1,2"})

	if !patGroupMembershipAccessAllowed(ctx, 7) {
		t.Fatal("PAT whitelist [1,2] covers all current members → must allow update")
	}
}

func TestPatHasGroupMembershipAccess_JWTAlwaysAllowed(t *testing.T) {
	db := newTestDB(t)
	swap := swapSingletonDB(t, db)
	defer swap()

	if err := db.Create(&model.ServerGroupServer{
		Common:        model.Common{ID: 1, UserID: 1},
		ServerGroupId: 100,
		ServerId:      99,
	}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin})

	if !patGroupMembershipAccessAllowed(ctx, 100) {
		t.Fatal("JWT requests (no PAT) must always pass — the existing admin/owner check stands")
	}
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ServerGroupServer{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func swapSingletonDB(t *testing.T, db *gorm.DB) func() {
	t.Helper()
	original := singleton.DB
	singleton.DB = db
	return func() { singleton.DB = original }
}
