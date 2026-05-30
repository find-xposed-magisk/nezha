package controller

// 回归 service monitor 运行时入口 (batchDeleteService) 上的 PAT
// cover-fanout 收口。与 cron_dispatch_pat_test.go 对称，钉死写侧
// rejectImplicitServiceCoverForLimitedPAT 与运行时
// enforcePATServiceDispatchScope 共用同一裁决路径。

import (
	"bytes"
	"encoding/json"
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

func setupServiceDispatchPATFixture(t *testing.T) {
	t.Helper()

	originalDB := singleton.DB
	originalCache := singleton.Cache
	originalLoc := singleton.Loc
	originalLocalizer := singleton.Localizer
	originalServer := singleton.ServerShared
	originalUserInfo := singleton.UserInfoMap
	originalSentinel := singleton.ServiceSentinelShared
	originalCron := singleton.CronShared

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Service{}, &model.Server{}, &model.User{}, &model.ServiceHistory{}))

	singleton.DB = db
	singleton.Loc = time.UTC
	singleton.Cache = cache.New(time.Minute, time.Minute)
	singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	// ServiceSentinel 在构造时会调 CronShared.AddFunc 注册每日/每周维护任务，
	// 必须先于 NewServiceSentinel 装配。
	singleton.CronShared = singleton.NewCronClass()

	sentinel, err := singleton.NewServiceSentinel(make(chan *model.Service, 4))
	require.NoError(t, err)
	singleton.ServiceSentinelShared = sentinel

	sc := singleton.NewEmptyServerClassForTest()
	for _, id := range []uint64{1, 2} {
		s := &model.Server{}
		s.ID = id
		s.SetUserID(100)
		sc.InsertForTest(s)
	}
	singleton.ServerShared = sc

	singleton.UserLock.Lock()
	singleton.UserInfoMap = map[uint64]model.UserInfo{100: {Role: model.RoleMember}}
	singleton.UserLock.Unlock()

	t.Cleanup(func() {
		sentinel.Close()
		singleton.ServiceSentinelShared = originalSentinel
		singleton.CronShared = originalCron
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

func insertServiceForDispatchTest(t *testing.T, cover uint8, skip map[uint64]bool) uint64 {
	t.Helper()
	svc := &model.Service{
		Common:      model.Common{UserID: 100},
		Name:        "dispatch-svc-fixture",
		Type:        model.TaskTypeTCPPing,
		Target:      "example.invalid:80",
		Duration:    30,
		Cover:       cover,
		SkipServers: skip,
	}
	require.NoError(t, singleton.DB.Create(svc).Error)
	require.NoError(t, singleton.ServiceSentinelShared.Update(svc))
	singleton.ServiceSentinelShared.UpdateServiceList()
	return svc.ID
}

func newServiceDispatchRouter(t *testing.T, tok *model.APIToken) *gin.Engine {
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
	r.POST("/api/v1/batch-delete/service", commonHandler(batchDeleteService))
	return r
}

func TestBatchDeleteService_RejectsCoverAllWithInsufficientSkipForLimitedPAT(t *testing.T) {
	setupServiceDispatchPATFixture(t)
	svcID := insertServiceForDispatchTest(t, model.ServiceCoverAll, map[uint64]bool{1: true})

	tok := &model.APIToken{ID: 31, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newServiceDispatchRouter(t, tok)
	body, _ := json.Marshal([]uint64{svcID})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/batch-delete/service", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.False(t, success,
		"PAT [1] must NOT batch-delete a ServiceCoverAll monitor whose SkipServers only marks whitelisted servers; DispatchTask still probes server 2")
	assert.Contains(t, errMsg, "permission denied")

	var rows []model.Service
	require.NoError(t, singleton.DB.Find(&rows).Error)
	assert.Len(t, rows, 1, "service row must still exist when the delete call is rejected")
}

func TestBatchDeleteService_AllowsCoverAllWhenSkipCoversNonWhitelisted(t *testing.T) {
	setupServiceDispatchPATFixture(t)
	svcID := insertServiceForDispatchTest(t, model.ServiceCoverAll, map[uint64]bool{2: true})

	tok := &model.APIToken{ID: 32, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newServiceDispatchRouter(t, tok)
	body, _ := json.Marshal([]uint64{svcID})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/batch-delete/service", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.True(t, success,
		"SkipServers covering every non-whitelisted owner server must allow batch-delete: error=%s", errMsg)

	var rows []model.Service
	require.NoError(t, singleton.DB.Find(&rows).Error)
	assert.Empty(t, rows, "service row must be deleted when the call succeeds")
}

func TestBatchDeleteService_AllowsCoverIgnoreAllInsideWhitelist(t *testing.T) {
	setupServiceDispatchPATFixture(t)
	svcID := insertServiceForDispatchTest(t, model.ServiceCoverIgnoreAll, map[uint64]bool{1: true})

	tok := &model.APIToken{ID: 33, UserID: 100}
	tok.SetServerIDs([]uint64{1})

	r := newServiceDispatchRouter(t, tok)
	body, _ := json.Marshal([]uint64{svcID})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/batch-delete/service", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	success, errMsg := decodeCommonResponseError(t, w.Body.Bytes())
	assert.True(t, success,
		"ServiceCoverIgnoreAll allow-list inside PAT whitelist must allow batch-delete: error=%s", errMsg)

	var rows []model.Service
	require.NoError(t, singleton.DB.Find(&rows).Error)
	assert.Empty(t, rows)
}
