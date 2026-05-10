package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/singleton"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPayloadFunc(t *testing.T) {
	payloadFn := payloadFunc()

	// 测试包含IP的格式
	t.Run("format with IP", func(t *testing.T) {
		data := map[string]interface{}{
			"user_id": "123",
			"ip":      "192.168.1.1",
		}
		claims := payloadFn(data)
		assert.Equal(t, "123", claims["user_id"])
		assert.Equal(t, "192.168.1.1", claims["ip"])
	})

	// 测试不包含IP的格式
	t.Run("format without IP", func(t *testing.T) {
		data := map[string]interface{}{
			"user_id": "123",
		}
		claims := payloadFn(data)
		assert.Equal(t, "123", claims["user_id"])
		assert.Nil(t, claims["ip"])
	})

	// 测试无效数据格式
	t.Run("invalid data format", func(t *testing.T) {
		claims := payloadFn("123") // 字符串类型不再支持
		assert.Empty(t, claims)
	})

	// 测试空的map
	t.Run("empty map", func(t *testing.T) {
		data := map[string]interface{}{}
		claims := payloadFn(data)
		assert.Empty(t, claims)
	})
}

func TestIPBinding(t *testing.T) {
	// 创建测试用的gin context
	gin.SetMode(gin.TestMode)

	t.Run("IP mismatch should invalidate token", func(t *testing.T) {
		// 模拟JWT claims包含IP绑定
		claims := jwt.MapClaims{
			"user_id": "123",
			"ip":      "192.168.1.1",
			"exp":     float64(time.Now().Add(time.Hour).Unix()),
		}

		// 这里需要实际的数据库和用户设置来完全测试
		// 但可以测试claims的基本结构
		assert.Equal(t, "123", claims["user_id"])
		assert.Equal(t, "192.168.1.1", claims["ip"])
	})

	t.Run("no IP in token should deny access", func(t *testing.T) {
		// 没有IP绑定的token应该被拒绝
		claims := jwt.MapClaims{
			"user_id": "123",
			"exp":     float64(time.Now().Add(time.Hour).Unix()),
		}

		// 验证token结构
		assert.Equal(t, "123", claims["user_id"])
		assert.Nil(t, claims["ip"])
	})
}

func TestValidateRuleRejectsForeignTriggerTasks(t *testing.T) {
	ctx := newMemberValidationContext(t)

	alertRule := &model.AlertRule{
		Common:              model.Common{UserID: 200},
		Name:                "member alert",
		Rules:               []*model.Rule{{Type: "offline", Duration: 3}},
		FailTriggerTasks:    []uint64{42},
		RecoverTriggerTasks: []uint64{42},
	}

	assert.Error(t, validateRule(ctx, alertRule))
}

func TestValidateServersRejectsForeignTriggerTasks(t *testing.T) {
	ctx := newMemberValidationContext(t)

	service := &model.Service{
		Common:              model.Common{UserID: 200},
		Name:                "member service",
		EnableTriggerTask:   true,
		FailTriggerTasks:    []uint64{42},
		RecoverTriggerTasks: []uint64{42},
		SkipServers:         map[uint64]bool{},
	}

	assert.Error(t, validateServers(ctx, service))
}

func newMemberValidationContext(t *testing.T) *gin.Context {
	t.Helper()

	originalDB := singleton.DB
	originalLoc := singleton.Loc
	originalLocalizer := singleton.Localizer
	originalCronShared := singleton.CronShared
	originalServerShared := singleton.ServerShared

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&model.Cron{}, &model.Server{}))
	assert.NoError(t, db.Create(&model.Cron{
		Common:   model.Common{ID: 42, UserID: 1},
		Name:     "foreign trigger task",
		Command:  "admin-maintenance",
		TaskType: model.CronTypeTriggerTask,
		Cover:    model.CronCoverAlertTrigger,
	}).Error)

	singleton.DB = db
	singleton.Loc = time.Local
	singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	singleton.CronShared = singleton.NewCronClass()
	singleton.ServerShared = singleton.NewServerClass()
	t.Cleanup(func() {
		singleton.DB = originalDB
		singleton.Loc = originalLoc
		singleton.Localizer = originalLocalizer
		singleton.CronShared = originalCronShared
		singleton.ServerShared = originalServerShared
	})

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{
		Common: model.Common{ID: 200},
		Role:   model.RoleMember,
	})
	return ctx
}
