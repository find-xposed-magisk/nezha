package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

func newCacheKeyCtx(t *testing.T, user *model.User, tok *model.APIToken) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/service", nil)
	if user != nil {
		c.Set(model.CtxKeyAuthorizedUser, user)
	}
	if tok != nil {
		c.Set(model.CtxKeyAPIToken, tok)
		c.Set(apiTokenCtxKey, tok)
	}
	return c
}

func TestServiceResponseCacheKey_DistinguishesPATsWithDifferentServerWhitelist(t *testing.T) {
	user := &model.User{Common: model.Common{ID: 100}, Role: model.RoleMember}

	tokA := &model.APIToken{ID: 1, UserID: 100}
	tokA.SetServerIDs([]uint64{7})

	tokB := &model.APIToken{ID: 2, UserID: 100}
	tokB.SetServerIDs([]uint64{8})

	keyA := serviceResponseCacheKey(newCacheKeyCtx(t, user, tokA))
	keyB := serviceResponseCacheKey(newCacheKeyCtx(t, user, tokB))

	if keyA == keyB {
		t.Fatalf("singleflight key must differ across PATs with disjoint server_ids; got %q for both",
			keyA)
	}
}

func TestServiceResponseCacheKey_DistinguishesPATFromJWT(t *testing.T) {
	user := &model.User{Common: model.Common{ID: 100}, Role: model.RoleMember}

	tok := &model.APIToken{ID: 1, UserID: 100}
	tok.SetServerIDs([]uint64{7})

	keyPAT := serviceResponseCacheKey(newCacheKeyCtx(t, user, tok))
	keyJWT := serviceResponseCacheKey(newCacheKeyCtx(t, user, nil))

	if keyPAT == keyJWT {
		t.Fatalf("PAT-shaped key must not collide with the JWT-shaped key; got %q for both", keyPAT)
	}
}
