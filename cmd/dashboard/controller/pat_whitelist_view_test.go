package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

// L1 regression: patHasServerWhitelist used to type-assert specifically to
// *model.APIToken, so any other APITokenAccessor that ALSO implements
// CanAccessServer/ServerIDs (test stubs, future wrappers) was silently
// treated as "not limited" by the cover-fanout guard. The check must use
// the APITokenWhitelistView interface instead.
type viewOnlyPAT struct {
	ids []uint64
}

func (v *viewOnlyPAT) CanAccessServer(id uint64) bool {
	for _, x := range v.ids {
		if x == id {
			return true
		}
	}
	return false
}

func (v *viewOnlyPAT) ServerIDs() []uint64 { return v.ids }

func TestPatHasServerWhitelist_RecognisesNonAPITokenWhitelistViewImplementor(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAPIToken, &viewOnlyPAT{ids: []uint64{1}})

	if !patHasServerWhitelist(ctx) {
		t.Fatal("any APITokenWhitelistView implementor with non-empty ServerIDs must be flagged as limited; otherwise non-*model.APIToken wrappers silently escape the cover-fanout guard")
	}
}

func TestPatHasServerWhitelist_EmptyWhitelistViaInterfaceCountsAsUnlimited(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAPIToken, &viewOnlyPAT{ids: nil})

	if patHasServerWhitelist(ctx) {
		t.Fatal("empty whitelist = unlimited (existing semantics); must continue to return false")
	}
}

func TestPatHasServerWhitelist_NoPATReturnsFalse(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	if patHasServerWhitelist(ctx) {
		t.Fatal("JWT requests (no PAT) must return false — there's no whitelist to escape")
	}
}

func TestPatHasServerWhitelist_RealAPITokenStillWorks(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAPIToken, &model.APIToken{ServersCSV: "1,2"})
	if !patHasServerWhitelist(ctx) {
		t.Fatal("real *model.APIToken with ServersCSV must still be flagged as limited (regression backstop)")
	}
}
