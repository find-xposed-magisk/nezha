package controller

// 回归 cron 运行时入口 (manualTriggerCron / batchDeleteCron) 上的 PAT
// cover-fanout 收口。配合 permissions_cover_fanout_test.go 的底座单测，
// 形成「共享底座 ↔ 资源专用入口」两层钉子，任何后续重构（例如把 guard
// 拆出 controller、把 cover 模式合并/拆分）都必须保留：
//   - 受限 PAT 不能通过 manualTrigger 触发一个 deny-list 不充分的
//     CronCoverAll → 否则 CronTrigger fan out 到白名单外 owner servers。
//   - 受限 PAT 不能通过 batchDelete 删除同样形态的 cron → 否则相当于
//     间接操作白名单外 owner servers 的调度策略。

import (
	"bytes"
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

// setupCronDispatchPATFixture 与 setupCoverPATFixture 同一拓扑：alice
// (uid=100) 拥有 server 1 / server 2；下游测试给出 PAT server_ids=[1]。
func setupCronDispatchPATFixture(t *testing.T) {
	t.Helper()

	originalDB := singleton.DB
	originalCache := singleton.Cache
	originalLoc := singleton.Loc
	originalLocalizer := singleton.Localizer
	originalCron := singleton.CronShared
	originalServer := singleton.ServerShared
	originalUserInfo := singleton.UserInfoMap
	originalNotification := singleton.NotificationShared

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Cron{}, &model.Server{}, &model.User{}, &model.NotificationGroup{}, &model.Notification{}))

	singleton.DB = db
	singleton.Loc = time.UTC
	singleton.Cache = cache.New(time.Minute, time.Minute)
	singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	// CronTrigger 的 fan-out 路径会在 server 没接入 task-stream 时调
	// NotificationShared.SendNotification 上报「离线」；这里给出一个空的
	// notification class，避免 nil deref。本测试不验证通知内容。
	singleton.NotificationShared = singleton.NewNotificationClass()

	sc := singleton.NewEmptyServerClassForTest()
	for _, id := range []uint64{1, 2} {
		s := &model.Server{}
		s.ID = id
		s.SetUserID(100)
		sc.InsertForTest(s)
	}
	singleton.ServerShared = sc
	singleton.CronShared = singleton.NewCronClass()

	singleton.UserLock.Lock()
	singleton.UserInfoMap = map[uint64]model.UserInfo{100: {Role: model.RoleMember}}
	singleton.UserLock.Unlock()

	t.Cleanup(func() {
		// Test-owned cron jobs must be joined before restoring process-global singleton dependencies.
		singleton.CronShared.Close()
		_ = sqlDB.Close()
		singleton.DB = originalDB
		singleton.Cache = originalCache
		singleton.Loc = originalLoc
		singleton.Localizer = originalLocalizer
		singleton.CronShared = originalCron
		singleton.ServerShared = originalServer
		singleton.NotificationShared = originalNotification
		singleton.UserLock.Lock()
		singleton.UserInfoMap = originalUserInfo
		singleton.UserLock.Unlock()
	})
}

func insertCronForDispatchTest(t *testing.T, cover uint8, servers []uint64) uint64 {
	t.Helper()
	cr := &model.Cron{
		Common:   model.Common{UserID: 100},
		Name:     "dispatch-fixture",
		TaskType: model.CronTypeCronTask,
		Command:  "echo dispatch",
		Servers:  servers,
		Cover:    cover,
	}
	require.NoError(t, singleton.DB.Create(cr).Error)
	singleton.CronShared.Update(cr)
	return cr.ID
}

func newCronDispatchRouter(t *testing.T, tok *model.APIToken) *gin.Engine {
	t.Helper()
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
	r.POST("/api/v1/batch-delete/cron", commonHandler(batchDeleteCron))
	return r
}

func TestManualTriggerCron_RejectsCoverAllWithInsufficientDenyForLimitedPAT(t *testing.T) {
	setupCronDispatchPATFixture(t)
	cronID := insertCronForDispatchTest(t, model.CronCoverAll, []uint64{1})

	tok := &model.APIToken{ID: 17, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronDispatchRouter(t, tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/cron/"+strconv.FormatUint(cronID, 10)+"/manual", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.False(t, success,
		"PAT [1] must NOT manually trigger a CronCoverAll cron whose deny-list does not cover owner server 2; CronTrigger would fan out to it")
	assert.Contains(t, errMsg, "permission denied")
}

func TestManualTriggerCron_AllowsCoverAllWhenDenyCoversNonWhitelisted(t *testing.T) {
	setupCronDispatchPATFixture(t)
	cronID := insertCronForDispatchTest(t, model.CronCoverAll, []uint64{2})

	tok := &model.APIToken{ID: 18, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronDispatchRouter(t, tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/cron/"+strconv.FormatUint(cronID, 10)+"/manual", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.True(t, success,
		"CronCoverAll whose deny-list covers every non-whitelisted owner server must remain triggerable: error=%s", errMsg)
}

func TestManualTriggerCron_AllowsCoverIgnoreAllInsideWhitelist(t *testing.T) {
	setupCronDispatchPATFixture(t)
	cronID := insertCronForDispatchTest(t, model.CronCoverIgnoreAll, []uint64{1})

	tok := &model.APIToken{ID: 19, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronDispatchRouter(t, tok)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/cron/"+strconv.FormatUint(cronID, 10)+"/manual", nil)
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.True(t, success,
		"CronCoverIgnoreAll allow-list inside PAT whitelist must trigger normally: error=%s", errMsg)
}

func TestBatchDeleteCron_RejectsCoverAllWithInsufficientDenyForLimitedPAT(t *testing.T) {
	setupCronDispatchPATFixture(t)
	cronID := insertCronForDispatchTest(t, model.CronCoverAll, []uint64{1})

	tok := &model.APIToken{ID: 21, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronDispatchRouter(t, tok)
	body, _ := json.Marshal([]uint64{cronID})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/batch-delete/cron", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.False(t, success,
		"PAT [1] must NOT batch-delete a CronCoverAll cron whose deny-list does not cover owner server 2")
	assert.Contains(t, errMsg, "permission denied")

	var rows []model.Cron
	require.NoError(t, singleton.DB.Find(&rows).Error)
	assert.Len(t, rows, 1, "cron row must still exist when the delete call is rejected")
}

func TestBatchDeleteCron_AllowsCoverAllWhenDenyCoversNonWhitelisted(t *testing.T) {
	setupCronDispatchPATFixture(t)
	cronID := insertCronForDispatchTest(t, model.CronCoverAll, []uint64{2})

	tok := &model.APIToken{ID: 22, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newCronDispatchRouter(t, tok)
	body, _ := json.Marshal([]uint64{cronID})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/batch-delete/cron", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.True(t, success,
		"deny-list covering every non-whitelisted owner server must allow batch-delete: error=%s", errMsg)

	var rows []model.Cron
	require.NoError(t, singleton.DB.Find(&rows).Error)
	assert.Empty(t, rows, "cron row must be deleted when the call succeeds")
}
