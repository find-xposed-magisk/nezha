package controller

import (
	"net/http"
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

func setupCronManualTriggerFixture(t *testing.T) {
	t.Helper()

	originalDB := singleton.DB
	originalCache := singleton.Cache
	originalLoc := singleton.Loc
	originalLocalizer := singleton.Localizer
	originalCron := singleton.CronShared
	originalServer := singleton.ServerShared
	originalUserInfo := singleton.UserInfoMap
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Cron{}, &model.Server{}, &model.User{}))

	singleton.DB = db
	singleton.Loc = time.UTC
	singleton.Cache = cache.New(time.Minute, time.Minute)
	singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	singleton.ServerShared = singleton.NewServerClass()
	singleton.CronShared = singleton.NewCronClass()
	singleton.UserLock.Lock()
	singleton.UserInfoMap = map[uint64]model.UserInfo{100: {Role: model.RoleMember}}
	singleton.UserLock.Unlock()

	cr := &model.Cron{
		Common:   model.Common{ID: 7, UserID: 100},
		Name:     "victim cron",
		TaskType: model.CronTypeCronTask,
		Command:  "echo csrf-poc",
		Cover:    model.CronCoverIgnoreAll,
	}
	require.NoError(t, db.Create(cr).Error)
	singleton.CronShared.Update(cr)

	t.Cleanup(func() {
		singleton.CronShared.Close()
		_ = sqlDB.Close()
		singleton.DB = originalDB
		singleton.Cache = originalCache
		singleton.Loc = originalLoc
		singleton.Localizer = originalLocalizer
		singleton.CronShared = originalCron
		singleton.ServerShared = originalServer
		singleton.UserLock.Lock()
		singleton.UserInfoMap = originalUserInfo
		singleton.UserLock.Unlock()
	})
}

func newCronManualRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(model.CtxKeyAuthorizedUser, &model.User{
			Common: model.Common{ID: 100},
			Role:   model.RoleMember,
		})
		c.Next()
	})
	r.POST("/api/v1/cron/:id/manual", commonHandler(manualTriggerCron))
	return r
}

func TestCronManualTriggerRejectsCrossSiteGET(t *testing.T) {
	setupCronManualTriggerFixture(t)
	r := newCronManualRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cron/7/manual", nil)
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code, "manual trigger must reject cross-site GET — the route is POST-only after the CSRF fix")
}

func TestCronManualTriggerAcceptsSameSitePOST(t *testing.T) {
	setupCronManualTriggerFixture(t)
	r := newCronManualRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cron/7/manual", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.True(t, success, "owner POST must succeed: error=%q", errMsg)
}
