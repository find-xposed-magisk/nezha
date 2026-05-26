package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/singleton"
)

func setupServerGroupVisibilityFixture(t *testing.T) {
	t.Helper()

	originalDB := singleton.DB
	originalCache := singleton.Cache
	originalLoc := singleton.Loc
	originalLocalizer := singleton.Localizer
	originalServer := singleton.ServerShared
	originalUserInfo := singleton.UserInfoMap

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Server{}, &model.ServerGroup{}, &model.ServerGroupServer{}, &model.User{}))

	singleton.DB = db
	singleton.Loc = time.UTC
	singleton.Cache = cache.New(time.Minute, time.Minute)
	singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	singleton.UserLock.Lock()
	singleton.UserInfoMap = map[uint64]model.UserInfo{
		1:   {Role: model.RoleAdmin},
		200: {Role: model.RoleMember},
	}
	singleton.UserLock.Unlock()

	require.NoError(t, db.Create(&model.Server{Common: model.Common{ID: 1, UserID: 1}, Name: "public", UUID: "public", HideForGuest: false}).Error)
	require.NoError(t, db.Create(&model.Server{Common: model.Common{ID: 2, UserID: 1}, Name: "hidden", UUID: "hidden", HideForGuest: true}).Error)

	require.NoError(t, db.Create(&model.ServerGroup{Common: model.Common{ID: 10, UserID: 1}, Name: "Public Group"}).Error)
	require.NoError(t, db.Create(&model.ServerGroup{Common: model.Common{ID: 11, UserID: 1}, Name: "Empty Group"}).Error)
	require.NoError(t, db.Create(&model.ServerGroupServer{Common: model.Common{UserID: 1}, ServerGroupId: 10, ServerId: 1}).Error)

	singleton.ServerShared = singleton.NewServerClass()

	t.Cleanup(func() {
		singleton.DB = originalDB
		singleton.Cache = originalCache
		singleton.Loc = originalLoc
		singleton.Localizer = originalLocalizer
		singleton.ServerShared = originalServer
		singleton.UserLock.Lock()
		singleton.UserInfoMap = originalUserInfo
		singleton.UserLock.Unlock()
	})
}

func newServerGroupCtx(viewer *model.User) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/server-group", nil)
	if viewer != nil {
		c.Set(model.CtxKeyAuthorizedUser, viewer)
	}
	return c
}

func collectGroupNames(items []*model.ServerGroupResponseItem) []string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Group.Name)
	}
	return names
}

func TestListServerGroupGuestSkipsGroupsWithoutVisibleServers(t *testing.T) {
	setupServerGroupVisibilityFixture(t)

	items, err := listServerGroup(newServerGroupCtx(nil))
	require.NoError(t, err)
	names := collectGroupNames(items)

	assert.ElementsMatch(t, []string{"Public Group"}, names,
		"a group with no guest-visible servers is meaningless to a guest UI and exposing its name leaks the existence of empty/hidden-only groups")
}

func TestListServerGroupAuthenticatedMemberSeesOwnEmptyGroup(t *testing.T) {
	setupServerGroupVisibilityFixture(t)

	require.NoError(t, singleton.DB.Create(&model.ServerGroup{Common: model.Common{ID: 12, UserID: 200}, Name: "member empty group"}).Error)

	items, err := listServerGroup(newServerGroupCtx(&model.User{
		Common: model.Common{ID: 200},
		Role:   model.RoleMember,
	}))
	require.NoError(t, err)
	names := collectGroupNames(items)

	assert.Contains(t, names, "member empty group", "owner must still see their own empty group")
}

func TestListServerGroupAdminSeesAllGroupsIncludingEmpty(t *testing.T) {
	setupServerGroupVisibilityFixture(t)

	items, err := listServerGroup(newServerGroupCtx(&model.User{
		Common: model.Common{ID: 1},
		Role:   model.RoleAdmin,
	}))
	require.NoError(t, err)
	names := collectGroupNames(items)

	assert.ElementsMatch(t, []string{"Public Group", "Empty Group"}, names,
		"admin must keep full visibility, including empty groups")
}
