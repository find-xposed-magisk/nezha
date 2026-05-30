package controller

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

func setupAlertRuleFanoutFixture(t *testing.T) {
	t.Helper()

	originalDB := singleton.DB
	originalCache := singleton.Cache
	originalLoc := singleton.Loc
	originalLocalizer := singleton.Localizer
	originalServer := singleton.ServerShared
	originalCron := singleton.CronShared
	originalUserInfo := singleton.UserInfoMap

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Server{}, &model.AlertRule{}, &model.Cron{}, &model.User{}))

	singleton.DB = db
	singleton.Loc = time.UTC
	singleton.Cache = cache.New(time.Minute, time.Minute)
	singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)
	singleton.UserLock.Lock()
	singleton.UserInfoMap = map[uint64]model.UserInfo{1: {Role: model.RoleAdmin}}
	singleton.UserLock.Unlock()

	require.NoError(t, db.Create(&model.Server{Common: model.Common{ID: 1, UserID: 1}, Name: "s1", UUID: "s1"}).Error)
	require.NoError(t, db.Create(&model.Server{Common: model.Common{ID: 2, UserID: 1}, Name: "s2", UUID: "s2"}).Error)

	singleton.ServerShared = singleton.NewServerClass()
	singleton.CronShared = singleton.NewCronClass()

	t.Cleanup(func() {
		singleton.DB = originalDB
		singleton.Cache = originalCache
		singleton.Loc = originalLoc
		singleton.Localizer = originalLocalizer
		singleton.ServerShared = originalServer
		singleton.CronShared = originalCron
		singleton.UserLock.Lock()
		singleton.UserInfoMap = originalUserInfo
		singleton.UserLock.Unlock()
	})
}

func newAlertRuleCtxWithPAT(t *testing.T, viewer *model.User, tok *model.APIToken, body any) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/alert-rule", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	if viewer != nil {
		c.Set(model.CtxKeyAuthorizedUser, viewer)
	}
	if tok != nil {
		c.Set(model.CtxKeyAPIToken, tok)
	}
	return c
}

// A server-limited PAT must not be able to create a RuleCoverAll rule with an
// empty Ignore (deny-list). Empty deny-list means "monitor every owner-visible
// server", which escapes the PAT's server_ids whitelist.
func TestCreateAlertRulePATCoverAllEmptyIgnoreRejected(t *testing.T) {
	setupAlertRuleFanoutFixture(t)

	tok := &model.APIToken{ID: 5, UserID: 1}
	tok.SetServerIDs([]uint64{1})

	form := map[string]any{
		"name":   "all-servers",
		"enable": false,
		"rules": []map[string]any{
			{"type": "offline", "cover": model.RuleCoverAll, "duration": 10},
		},
	}

	c := newAlertRuleCtxWithPAT(t, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}, tok, form)
	_, err := createAlertRule(c)
	require.Error(t, err, "CoverAll + empty Ignore must be rejected for a PAT scoped to {1}")

	var count int64
	require.NoError(t, singleton.DB.Model(&model.AlertRule{}).Count(&count).Error)
	assert.Equal(t, int64(0), count, "no alert rule should be persisted")
}

// The same PAT may create a RuleCoverAll rule when it explicitly denies every
// server outside its whitelist (here: server 2), since the fan-out is then
// confined to server 1. Exercised at validateRule to avoid the alert-sentinel
// side effects of a full createAlertRule.
func TestValidateRulePATCoverAllDenyingOutsideServersAllowed(t *testing.T) {
	setupAlertRuleFanoutFixture(t)

	tok := &model.APIToken{ID: 6, UserID: 1}
	tok.SetServerIDs([]uint64{1})

	c := newAlertRuleCtxWithPAT(t, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}, tok, nil)

	r := &model.AlertRule{
		Common: model.Common{UserID: 1},
		Name:   "only-server-1",
		Rules:  []*model.Rule{{Type: "offline", Cover: model.RuleCoverAll, Duration: 10, Ignore: map[uint64]bool{2: true}}},
	}
	require.NoError(t, validateRule(c, r), "CoverAll denying every out-of-whitelist server must be allowed")
}

// Empty deny-list at validateRule level must also be rejected.
func TestValidateRulePATCoverAllEmptyIgnoreRejected(t *testing.T) {
	setupAlertRuleFanoutFixture(t)

	tok := &model.APIToken{ID: 7, UserID: 1}
	tok.SetServerIDs([]uint64{1})

	c := newAlertRuleCtxWithPAT(t, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}, tok, nil)

	r := &model.AlertRule{
		Common: model.Common{UserID: 1},
		Name:   "all",
		Rules:  []*model.Rule{{Type: "offline", Cover: model.RuleCoverAll, Duration: 10}},
	}
	require.Error(t, validateRule(c, r), "CoverAll + empty Ignore must be rejected for PAT scoped to {1}")
}
