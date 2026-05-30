package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
)

func ensureNezhaSingleton(t *testing.T) {
	t.Helper()
	if rpc.NezhaHandlerSingleton == nil {
		rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	}
}

// H2 regression: terminal/FM stream attachment must respect the caller PAT's
// server_ids whitelist. The existing IsStreamAuthorizedForUser only gates on
// creator-id / admin role, so an admin's server-limited PAT could attach to
// a stream targeting any server simply by knowing the streamId.
func TestStreamAttachAllowedForRequest_DeniesPATOutsideWhitelist(t *testing.T) {
	ensureNezhaSingleton(t)
	streamId := "stream-h2-deny"
	rpc.NezhaHandlerSingleton.CreateStream(streamId, 1, 99)
	t.Cleanup(func() { _ = rpc.NezhaHandlerSingleton.CloseStream(streamId) })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin})
	ctx.Set(model.CtxKeyAPIToken, &model.APIToken{ServersCSV: "1"}) // does NOT include 99

	if streamAttachAllowedForRequest(ctx, streamId) {
		t.Fatal("admin PAT scoped to [1] must NOT attach to a stream targeting server 99")
	}
}

func TestStreamAttachAllowedForRequest_AllowsPATInsideWhitelist(t *testing.T) {
	ensureNezhaSingleton(t)
	streamId := "stream-h2-allow"
	rpc.NezhaHandlerSingleton.CreateStream(streamId, 1, 5)
	t.Cleanup(func() { _ = rpc.NezhaHandlerSingleton.CloseStream(streamId) })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin})
	ctx.Set(model.CtxKeyAPIToken, &model.APIToken{ServersCSV: "5"})

	if !streamAttachAllowedForRequest(ctx, streamId) {
		t.Fatal("PAT scoped to [5] must attach to a stream targeting server 5")
	}
}

func TestStreamAttachAllowedForRequest_JWTAdminUnchanged(t *testing.T) {
	ensureNezhaSingleton(t)
	streamId := "stream-h2-jwt"
	rpc.NezhaHandlerSingleton.CreateStream(streamId, 1, 99)
	t.Cleanup(func() { _ = rpc.NezhaHandlerSingleton.CloseStream(streamId) })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin})

	if !streamAttachAllowedForRequest(ctx, streamId) {
		t.Fatal("JWT admin (no PAT) must continue to attach via the existing admin branch")
	}
}

func TestStreamAttachAllowedForRequest_DeniesNonCreatorMember(t *testing.T) {
	ensureNezhaSingleton(t)
	streamId := "stream-h2-foreign"
	rpc.NezhaHandlerSingleton.CreateStream(streamId, 1, 5)
	t.Cleanup(func() { _ = rpc.NezhaHandlerSingleton.CloseStream(streamId) })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 2}, Role: model.RoleMember})

	if streamAttachAllowedForRequest(ctx, streamId) {
		t.Fatal("non-creator non-admin member must remain denied (pre-existing GHSA gate)")
	}
}

func TestStreamAttachAllowedForRequest_UnknownStreamRejected(t *testing.T) {
	ensureNezhaSingleton(t)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 1}, Role: model.RoleAdmin})

	if streamAttachAllowedForRequest(ctx, "does-not-exist") {
		t.Fatal("unknown streamId must remain rejected")
	}
}
