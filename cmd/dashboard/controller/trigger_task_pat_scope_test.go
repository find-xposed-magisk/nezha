package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func newTriggerTaskCtxWithPAT(viewer *model.User, tok *model.APIToken) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/service", http.NoBody)
	if viewer != nil {
		c.Set(model.CtxKeyAuthorizedUser, viewer)
	}
	if tok != nil {
		c.Set(model.CtxKeyAPIToken, tok)
		c.Set(apiTokenCtxKey, tok)
	}
	return c
}

// 注册一个用户 1 拥有的触发任务，使 CronShared.CheckPermission 通过，
// 从而隔离出「PAT 缺少 cron:exec」这一条裁决路径。
func registerOwnerTriggerTask(t *testing.T, id uint64) {
	t.Helper()
	singleton.CronShared.Update(&model.Cron{
		Common:   model.Common{ID: id, UserID: 1},
		Name:     "trigger",
		TaskType: model.CronTypeTriggerTask,
		Cover:    model.CronCoverAll,
	})
}

func TestValidateServersPATTriggerTaskRequiresCronExec(t *testing.T) {
	setupAlertRuleFanoutFixture(t)
	registerOwnerTriggerTask(t, 100)

	viewer := &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}

	noExec := &model.APIToken{ID: 10, UserID: 1}
	noExec.SetScopes([]string{model.ScopeServiceWrite})
	svc := &model.Service{
		Common:            model.Common{UserID: 1},
		EnableTriggerTask: true,
		FailTriggerTasks:  []uint64{100},
	}
	require.Error(t, validateServers(newTriggerTaskCtxWithPAT(viewer, noExec), svc),
		"service:write PAT must not bind a trigger task without cron:exec")

	withExec := &model.APIToken{ID: 11, UserID: 1}
	withExec.SetScopes([]string{model.ScopeServiceWrite, model.ScopeCronExec})
	require.NoError(t, validateServers(newTriggerTaskCtxWithPAT(viewer, withExec), svc),
		"service:write + cron:exec PAT may bind a trigger task")
}

func TestValidateRulePATTriggerTaskRequiresCronExec(t *testing.T) {
	setupAlertRuleFanoutFixture(t)
	registerOwnerTriggerTask(t, 200)

	viewer := &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}

	noExec := &model.APIToken{ID: 12, UserID: 1}
	noExec.SetScopes([]string{model.ScopeAlertRuleWrite})
	rule := &model.AlertRule{
		Common:              model.Common{UserID: 1},
		Name:                "r",
		Rules:               []*model.Rule{{Type: "offline", Cover: model.RuleCoverAll, Duration: 10, Ignore: map[uint64]bool{}}},
		RecoverTriggerTasks: []uint64{200},
	}
	require.Error(t, validateRule(newTriggerTaskCtxWithPAT(viewer, noExec), rule),
		"alertrule:write PAT must not bind a trigger task without cron:exec")

	withExec := &model.APIToken{ID: 13, UserID: 1}
	withExec.SetScopes([]string{model.ScopeAlertRuleWrite, model.ScopeCronExec})
	require.NoError(t, validateRule(newTriggerTaskCtxWithPAT(viewer, withExec), rule),
		"alertrule:write + cron:exec PAT may bind a trigger task")
}

// JWT 调用者（无 PAT）不受 cron:exec 收口影响。
func TestValidateServersJWTUnaffectedByTriggerTaskScope(t *testing.T) {
	setupAlertRuleFanoutFixture(t)
	registerOwnerTriggerTask(t, 300)

	viewer := &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin}
	svc := &model.Service{
		Common:            model.Common{UserID: 1},
		EnableTriggerTask: true,
		FailTriggerTasks:  []uint64{300},
	}
	require.NoError(t, validateServers(newTriggerTaskCtxWithPAT(viewer, nil), svc),
		"JWT caller must not be gated by cron:exec")
}
