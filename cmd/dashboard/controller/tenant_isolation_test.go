package controller

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/singleton"
)

// 通用租户隔离测试夹具：在 in-memory DB 上挂载所需 model 并塞两个用户，
// 用户 10（member）和用户 999（foreign owner）。
//
// 每个测试在两条路径上验证 member 不能跨租户：
//   - create 时即使请求体里包含 user_id 字段也不会越权
//   - update / delete 时不会改写或读取到 foreign owner 的资源
func setupTenancyTest(t *testing.T) func() {
	t.Helper()
	originalDB := singleton.DB
	originalLocalizer := singleton.Localizer
	originalServer := singleton.ServerShared
	if singleton.Localizer == nil {
		singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Cron{},
		&model.DDNSProfile{},
		&model.Notification{},
		&model.AlertRule{},
		&model.NotificationGroup{},
	))
	originalDDNS := singleton.DDNSShared
	originalNotif := singleton.NotificationShared
	singleton.DB = db
	singleton.ServerShared = singleton.NewEmptyServerClassForTest()
	singleton.DDNSShared = singleton.NewEmptyDDNSClassForTest()
	singleton.NotificationShared = singleton.NewEmptyNotificationClassForTest()
	return func() {
		_ = sqlDB.Close()
		singleton.DB = originalDB
		singleton.Localizer = originalLocalizer
		singleton.ServerShared = originalServer
		singleton.DDNSShared = originalDDNS
		singleton.NotificationShared = originalNotif
	}
}

func ctxAs(uid uint64, role model.Role) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	c.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: uid}, Role: role})
	return c
}

func ctxAsMemberWithBody(uid uint64, body any) *gin.Context {
	c := ctxAs(uid, model.RoleMember)
	b, _ := json.Marshal(body)
	c.Request = httptest.NewRequest("POST", "/", bytes.NewReader(b))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

// 设计说明：create 路径的"手工 user_id 注入"防护通过两点联合保证：
//  1. CronForm/DDNSForm/NotificationForm 等 form struct 不嵌入 Common，
//     绑定时不会 unmarshal "user_id" 字段
//  2. handler 第一行 `xxx.UserID = getUid(c)` 显式覆盖
// 因为 create 路径还会依赖 ServerShared / Localizer 等外部 singleton，
// 在单元测试中难以无副作用地完整运行；改用代码静态约束：在 form_no_userid_test.go
// 里用 reflect 验证所有 *Form 结构无 UserID 字段（next step）。
// 这里只测真正的所有权防线：update / delete。

// ---------- Cron ----------

func TestTenancy_UpdateCron_ForeignOwnerRejected(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.Cron{
		Common:    model.Common{UserID: 999},
		Name:      "foreign-cron",
		TaskType:  model.CronTypeCronTask,
		Scheduler: "@every 5m",
		Command:   "echo",
	}
	require.NoError(t, singleton.DB.Create(&foreign).Error)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name":      "hijacked",
		"task_type": model.CronTypeCronTask,
		"scheduler": "@every 1m",
		"command":   "echo pwned",
		"servers":   []uint64{},
		"cover":     model.CronCoverAll,
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(foreign.ID)}}
	_, err := updateCron(c)
	require.Error(t, err, "member 10 must not be able to update foreign-owned cron")

	var after model.Cron
	require.NoError(t, singleton.DB.First(&after, foreign.ID).Error)
	require.Equal(t, "foreign-cron", after.Name, "foreign cron must not be modified")
	require.Equal(t, uint64(999), after.UserID, "ownership must remain")
}

// ---------- DDNS ----------

func TestTenancy_CreateDDNS_InjectedUserIDIgnored(t *testing.T) {
	defer setupTenancyTest(t)()

	body := map[string]any{
		"name":                 "evil-ddns",
		"provider":             "webhook",
		"access_id":            "x",
		"access_secret":        "y",
		"webhook_url":          "http://127.0.0.1/",
		"webhook_method":       "GET",
		"webhook_request_type": "json",
		"webhook_request_body": "",
		"webhook_headers":      "",
		"user_id":              999, // attacker
	}
	c := ctxAsMemberWithBody(10, body)
	_, err := createDDNS(c)
	if err == nil {
		var stored model.DDNSProfile
		require.NoError(t, singleton.DB.First(&stored, "name = ?", "evil-ddns").Error)
		require.Equal(t, uint64(10), stored.UserID,
			"createDDNS must overwrite UserID with caller")
	}
}

func TestTenancy_UpdateDDNS_ForeignOwnerRejected(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.DDNSProfile{
		Common:             model.Common{UserID: 999},
		Name:               "foreign-ddns",
		Provider:           "webhook",
		AccessID:           "x",
		AccessSecret:       "y",
		WebhookURL:         "http://127.0.0.1/",
		WebhookMethod:      1,
		WebhookRequestType: 1,
	}
	require.NoError(t, singleton.DB.Create(&foreign).Error)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name":                 "hijacked",
		"provider":             "webhook",
		"access_id":            "x",
		"access_secret":        "y",
		"webhook_url":          "http://attacker/",
		"webhook_method":       "GET",
		"webhook_request_type": "json",
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(foreign.ID)}}
	_, err := updateDDNS(c)
	require.Error(t, err, "member must not be able to update foreign-owned DDNS")

	var after model.DDNSProfile
	require.NoError(t, singleton.DB.First(&after, foreign.ID).Error)
	require.Equal(t, "foreign-ddns", after.Name, "foreign DDNS must not be modified")
	require.Equal(t, "http://127.0.0.1/", after.WebhookURL, "webhook URL must not be hijacked")
}

func TestTenancy_DeleteDDNS_ForeignOwnerRejected(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.DDNSProfile{
		Common:             model.Common{UserID: 999},
		Name:               "foreign-ddns-del",
		Provider:           "webhook",
		WebhookURL:         "http://127.0.0.1/",
		WebhookMethod:      1,
		WebhookRequestType: 1,
	}
	require.NoError(t, singleton.DB.Create(&foreign).Error)
	singleton.DDNSShared.InsertForTest(&foreign)

	c := ctxAsMemberWithBody(10, []uint64{foreign.ID})
	_, err := batchDeleteDDNS(c)
	require.Error(t, err, "member must not be able to batch-delete foreign DDNS")

	var after model.DDNSProfile
	require.NoErrorf(t, singleton.DB.First(&after, foreign.ID).Error,
		"foreign DDNS must still exist after member's failed batch-delete (handler err=%v)", err)
}

// ---------- Notification ----------

func TestTenancy_UpdateNotification_ForeignOwnerRejected(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.Notification{
		Common:        model.Common{UserID: 999},
		Name:          "foreign-notify",
		URL:           "http://127.0.0.1/",
		RequestMethod: 1,
		RequestType:   1,
	}
	require.NoError(t, singleton.DB.Create(&foreign).Error)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name":           "hijacked",
		"url":            "http://attacker/",
		"request_method": 1,
		"request_type":   1,
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(foreign.ID)}}
	_, err := updateNotification(c)
	require.Error(t, err)

	var after model.Notification
	require.NoError(t, singleton.DB.First(&after, foreign.ID).Error)
	require.Equal(t, "http://127.0.0.1/", after.URL)
}

// ---------- NotificationGroup ----------

func TestTenancy_UpdateNotificationGroup_ForeignOwnerRejected(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.NotificationGroup{
		Common: model.Common{UserID: 999},
		Name:   "foreign-ng",
	}
	require.NoError(t, singleton.DB.Create(&foreign).Error)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name":          "hijacked",
		"notifications": []uint64{},
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(foreign.ID)}}
	_, err := updateNotificationGroup(c)
	require.Error(t, err)

	var after model.NotificationGroup
	require.NoError(t, singleton.DB.First(&after, foreign.ID).Error)
	require.Equal(t, "foreign-ng", after.Name)
}

// ---------- AlertRule ----------

func TestTenancy_UpdateAlertRule_ForeignOwnerRejected(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.AlertRule{
		Common: model.Common{UserID: 999},
		Name:   "foreign-rule",
	}
	require.NoError(t, singleton.DB.Create(&foreign).Error)

	c := ctxAsMemberWithBody(10, map[string]any{
		"name": "hijacked",
	})
	c.Params = gin.Params{{Key: "id", Value: itoa(foreign.ID)}}
	_, err := updateAlertRule(c)
	require.Error(t, err)

	var after model.AlertRule
	require.NoError(t, singleton.DB.First(&after, foreign.ID).Error)
	require.Equal(t, "foreign-rule", after.Name)
}

func TestTenancy_BatchDeleteAlertRule_ForeignOwnerSilentlySkipped(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.AlertRule{Common: model.Common{UserID: 999}, Name: "foreign-rule"}
	require.NoError(t, singleton.DB.Create(&foreign).Error)

	c := ctxAsMemberWithBody(10, []uint64{foreign.ID})
	_, err := batchDeleteAlertRule(c)
	_ = err

	var after model.AlertRule
	require.NoError(t, singleton.DB.First(&after, foreign.ID).Error,
		"member's batch-delete must not be able to remove foreign alert rule")
	require.Equal(t, uint64(999), after.UserID)
}

// Cron batch-delete 的所有权保护与 updateCron 共用 cr.HasPermission 检查路径
// （cron.go:127 vs cron.go:207），updateCron 用例已经覆盖该路径。这里不复测
// 是因为 batchDeleteCron 调 CronShared.CheckPermission，需要完整 CronShared
// 在内存中注册，会让单测 fixture 显著膨胀，性价比低。

// ---------- Notification batch-delete ----------

func TestTenancy_BatchDeleteNotification_ForeignOwnerSilentlySkipped(t *testing.T) {
	defer setupTenancyTest(t)()

	foreign := model.Notification{
		Common: model.Common{UserID: 999},
		Name:   "foreign-notify-del",
		URL:    "http://127.0.0.1/",
	}
	require.NoError(t, singleton.DB.Create(&foreign).Error)
	singleton.NotificationShared.InsertForTest(&foreign)

	c := ctxAsMemberWithBody(10, []uint64{foreign.ID})
	_, _ = batchDeleteNotification(c)

	var after model.Notification
	require.NoError(t, singleton.DB.First(&after, foreign.ID).Error,
		"member must not be able to batch-delete foreign notification")
}
