package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/i18n"
	"github.com/nezhahq/nezha/service/singleton"
)

func setupCronUpdateOwnerUIDFixture(t *testing.T) {
	t.Helper()

	originalCache := singleton.Cache
	originalLoc := singleton.Loc
	originalLocalizer := singleton.Localizer
	originalServer := singleton.ServerShared

	singleton.Loc = time.UTC
	singleton.Cache = cache.New(time.Minute, time.Minute)
	singleton.Localizer = i18n.NewLocalizer("en_US", "nezha", "translations", i18n.Translations)

	sc := singleton.NewEmptyServerClassForTest()
	for _, id := range []uint64{1, 2} {
		s := &model.Server{}
		s.ID = id
		s.SetUserID(100)
		sc.InsertForTest(s)
	}
	adminServer := &model.Server{}
	adminServer.ID = 5
	adminServer.SetUserID(200)
	sc.InsertForTest(adminServer)
	singleton.ServerShared = sc

	t.Cleanup(func() {
		singleton.Cache = originalCache
		singleton.Loc = originalLoc
		singleton.Localizer = originalLocalizer
		singleton.ServerShared = originalServer
	})
}

func newCtxAsAdminWithLimitedPAT(t *testing.T, callerUID uint64, whitelist []uint64) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(model.CtxKeyAuthorizedUser, &model.User{
		Common: model.Common{ID: callerUID},
		Role:   model.RoleAdmin,
	})
	tok := &model.APIToken{ID: 33, UserID: callerUID}
	tok.SetServerIDs(whitelist)
	c.Set(apiTokenCtxKey, tok)
	c.Set(model.CtxKeyAPIToken, tok)
	return c
}

// Threat: updateCron currently calls
//
//	rejectImplicitCoverForLimitedPAT(c, cf.Cover, cf.Servers)
//
// which internally resolves the owner UID via getUid(c) (caller id). When an
// admin uses a server-limited PAT to flip a *foreign* cron to CoverAll with
// an under-specified deny-list, the helper validates the deny-list against
// the admin's own servers, not the cron owner's. The admin's only owned
// server is 5 and it's already in the whitelist, so the guard returns nil
// even though CronTrigger will fan out to the cron owner's servers 1 and 2
// — both outside the PAT whitelist. The correct owner is the existing
// cron.UserID, not the caller. This test calls the helper directly with the
// cron owner uid and pins the safe behaviour.
func TestRejectImplicitCoverForLimitedPAT_RejectsCallerWhenCronOwnerHasUncoveredServers(t *testing.T) {
	setupCronUpdateOwnerUIDFixture(t)

	c := newCtxAsAdminWithLimitedPAT(t, 200, []uint64{5})

	const cronOwnerUID = uint64(100)
	err := rejectImplicitCoverForLimitedPATWithOwner(c, model.CronCoverAll, nil, cronOwnerUID)
	require.Error(t, err,
		"limited PAT must NOT pass cover-all check when the cron owner has servers outside the PAT whitelist; caller uid must not be used as owner")
	assert.Contains(t, err.Error(), "permission denied")
}

// Pins the safe path: same helper, but caller uid happens to equal the cron
// owner and the deny-list covers every owner-visible server outside the
// whitelist. Prevents regressing the helper into a blanket "always deny
// limited PAT" form.
func TestRejectImplicitCoverForLimitedPAT_AllowsCallerWhenDenyListCoversEveryOwnerServerOutsideWhitelist(t *testing.T) {
	setupCronUpdateOwnerUIDFixture(t)

	c := newCtxAsAdminWithLimitedPAT(t, 100, []uint64{1})

	err := rejectImplicitCoverForLimitedPATWithOwner(c, model.CronCoverAll, []uint64{2}, 100)
	require.NoError(t, err,
		"deny-list [2] covers every server uid 100 owns outside the PAT whitelist [1]; must pass")
}
