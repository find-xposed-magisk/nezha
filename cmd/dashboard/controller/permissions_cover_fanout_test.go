package controller

// 共享底座 assertPATCoverFanoutWithinWhitelist 的单元测试。
//
// 这一层不知道 cron / service，只知道三种 coverMode；测试矩阵覆盖
// {JWT / 无白名单 PAT / 有白名单 PAT × 充分 deny / 不充分 deny / allow-list
// 内 / 越界}，钉死「写侧 rejectImplicit* 与运行时 enforce* 必须共用同一裁
// 决路径」这条不变量。任何后续重构改动了规则但忘了同步两侧，这里会先于
// 资源专用入口测试暴露问题。

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func setupCoverFanoutFixture(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ensureLocalizerForStreamTests(t)

	originalServer := singleton.ServerShared
	sc := singleton.NewEmptyServerClassForTest()
	for _, id := range []uint64{1, 2, 3} {
		s := &model.Server{}
		s.ID = id
		s.SetUserID(100)
		sc.InsertForTest(s)
	}
	other := &model.Server{}
	other.ID = 9
	other.SetUserID(200)
	sc.InsertForTest(other)
	singleton.ServerShared = sc

	t.Cleanup(func() { singleton.ServerShared = originalServer })
}

func ctxWithPAT(t *testing.T, tok *model.APIToken) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(nil)
	if tok != nil {
		c.Set(model.CtxKeyAPIToken, tok)
		c.Set(apiTokenCtxKey, tok)
	}
	return c
}

func TestAssertPATCoverFanout_JWTAlwaysPasses(t *testing.T) {
	setupCoverFanoutFixture(t)
	c := ctxWithPAT(t, nil)

	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllMinusDeny, nil))
	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllowList, []uint64{2, 3}))
	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModePinnedByCaller, []uint64{2, 3}))
}

func TestAssertPATCoverFanout_UnscopedPATAlwaysPasses(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	c := ctxWithPAT(t, tok)

	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllMinusDeny, nil),
		"PAT without server whitelist must not be restricted by cover-fanout guard")
	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllowList, []uint64{2, 3}))
}

func TestAssertPATCoverFanout_AllMinusDeny_RejectsInsufficientDeny(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{1})
	c := ctxWithPAT(t, tok)

	err := assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllMinusDeny, []uint64{1})
	assert.Error(t, err, "deny-list covering only whitelisted server 1 still fans out to owner servers 2/3")

	err = assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllMinusDeny, []uint64{2})
	assert.Error(t, err, "deny-list missing owner server 3 must be rejected")
}

func TestAssertPATCoverFanout_AllMinusDeny_AcceptsSufficientDeny(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{1})
	c := ctxWithPAT(t, tok)

	err := assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllMinusDeny, []uint64{2, 3})
	assert.NoError(t, err, "deny-list covers every owner server outside the PAT whitelist; must pass")
}

func TestAssertPATCoverFanout_AllowList_RejectsOutsideWhitelist(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{1})
	c := ctxWithPAT(t, tok)

	err := assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllowList, []uint64{1, 2})
	assert.Error(t, err, "allow-list containing non-whitelisted server 2 must be rejected")
}

func TestAssertPATCoverFanout_AllowList_AcceptsInsideWhitelist(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{1})
	c := ctxWithPAT(t, tok)

	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllowList, []uint64{1}))
	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModeAllowList, nil),
		"empty allow-list is the degenerate matches-nothing case; not a bypass")
}

func TestAssertPATCoverFanout_PinnedByCaller_PassesAlways(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{1})
	c := ctxWithPAT(t, tok)

	require.NoError(t, assertPATCoverFanoutWithinWhitelist(c, 100, coverModePinnedByCaller, []uint64{2, 3}),
		"alert-trigger dispatch pins the target server at fire time; assertPATCoverFanoutWithinWhitelist must not pre-judge")
}

func TestCronCoverMode_KnownValues(t *testing.T) {
	assert.Equal(t, coverModeAllMinusDeny, cronCoverMode(model.CronCoverAll))
	assert.Equal(t, coverModeAllowList, cronCoverMode(model.CronCoverIgnoreAll))
	assert.Equal(t, coverModePinnedByCaller, cronCoverMode(model.CronCoverAlertTrigger))
}

func TestServiceCoverMode_KnownValues(t *testing.T) {
	assert.Equal(t, coverModeAllMinusDeny, serviceCoverMode(model.ServiceCoverAll))
	assert.Equal(t, coverModeAllowList, serviceCoverMode(model.ServiceCoverIgnoreAll))
}

func TestSkipServersToDenyList_FiltersOnlyTrue(t *testing.T) {
	got := skipServersToDenyList(map[uint64]bool{1: true, 2: false, 3: true})
	assert.ElementsMatch(t, []uint64{1, 3}, got,
		"only true entries are real skips; false-valued entries must not be promoted to deny-list")
}

// 资源专用入口在底座上薄包装的契约：cron-runtime 与 service-runtime 必须
// 调底座，因此底座在「不充分 deny-list」时返回的 error 必须穿透到入口。
func TestEnforcePATCronDispatchScope_RelaysBaseDecision(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{1})
	c := ctxWithPAT(t, tok)

	cr := &model.Cron{
		Common:  model.Common{UserID: 100},
		Cover:   model.CronCoverAll,
		Servers: []uint64{1},
	}
	err := enforcePATCronDispatchScope(c, cr)
	assert.Error(t, err, "cover-all cron whose deny-list only covers whitelisted server must be rejected")

	cr.Servers = []uint64{2, 3}
	require.NoError(t, enforcePATCronDispatchScope(c, cr),
		"deny-list covering every non-whitelisted owner server must pass")
}

func TestEnforcePATServiceDispatchScope_RelaysBaseDecision(t *testing.T) {
	setupCoverFanoutFixture(t)
	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{1})
	c := ctxWithPAT(t, tok)

	svc := &model.Service{
		Common:      model.Common{UserID: 100},
		Cover:       model.ServiceCoverAll,
		SkipServers: map[uint64]bool{1: true},
	}
	err := enforcePATServiceDispatchScope(c, svc)
	assert.Error(t, err, "cover-all service whose SkipServers only marks whitelisted servers must be rejected")

	svc.SkipServers = map[uint64]bool{2: true, 3: true}
	require.NoError(t, enforcePATServiceDispatchScope(c, svc),
		"SkipServers covering every non-whitelisted owner server must pass")
}
