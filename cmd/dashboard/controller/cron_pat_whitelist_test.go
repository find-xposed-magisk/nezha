package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func setupCronPATWhitelistFixture(t *testing.T) (cronID7, cronID8 uint64) {
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

	cr7 := &model.Cron{
		Common:   model.Common{UserID: 100},
		Name:     "cron-on-server-1",
		TaskType: model.CronTypeCronTask,
		Command:  "echo s1",
		Servers:  []uint64{1},
		Cover:    model.CronCoverIgnoreAll,
	}
	cr8 := &model.Cron{
		Common:   model.Common{UserID: 100},
		Name:     "cron-on-server-2",
		TaskType: model.CronTypeCronTask,
		Command:  "echo s2",
		Servers:  []uint64{2},
		Cover:    model.CronCoverIgnoreAll,
	}
	require.NoError(t, db.Create(cr7).Error)
	require.NoError(t, db.Create(cr8).Error)
	singleton.CronShared.Update(cr7)
	singleton.CronShared.Update(cr8)

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

	return cr7.ID, cr8.ID
}

func newCronPATRouter(tok *model.APIToken) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		setAuthUser(c, 100, model.RoleMember)
		if tok != nil {
			c.Set(model.CtxKeyAPIToken, tok)
			c.Set(apiTokenCtxKey, tok)
		}
		c.Next()
	})
	r.POST("/api/v1/cron/:id/manual", commonHandler(manualTriggerCron))
	r.GET("/api/v1/cron", listHandler(listCron))
	return r
}

func TestCronManualTrigger_DeniesServerOutsidePATWhitelist(t *testing.T) {
	_, cron8 := setupCronPATWhitelistFixture(t)

	tok := &model.APIToken{ID: 17, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronPATRouter(tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/cron/"+strconv.FormatUint(cron8, 10)+"/manual", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.False(t, success,
		"PAT whitelist [1] must not allow triggering a cron bound to server 2")
	assert.Contains(t, errMsg, "permission denied")
}

func TestCronManualTrigger_AllowsServerInsidePATWhitelist(t *testing.T) {
	cron7, _ := setupCronPATWhitelistFixture(t)

	tok := &model.APIToken{ID: 17, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronPATRouter(tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/cron/"+strconv.FormatUint(cron7, 10)+"/manual", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.True(t, success,
		"PAT whitelist [1] must still allow triggering a cron bound to server 1: error=%s", errMsg)
}

func TestListCron_HidesRowsForServersOutsidePATWhitelist(t *testing.T) {
	cron7, cron8 := setupCronPATWhitelistFixture(t)

	tok := &model.APIToken{ID: 17, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronPATRouter(tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cron", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Success bool          `json:"success"`
		Error   string        `json:"error"`
		Data    []*model.Cron `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success, resp.Error)

	seen := map[uint64]bool{}
	for _, c := range resp.Data {
		seen[c.ID] = true
	}
	assert.True(t, seen[cron7], "cron bound to whitelisted server 1 must remain visible")
	assert.False(t, seen[cron8],
		"cron bound to non-whitelisted server 2 must be hidden from PAT view (rows=%+v)", resp.Data)
}
