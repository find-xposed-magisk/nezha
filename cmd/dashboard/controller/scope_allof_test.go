package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

// H3 regression: file-manager sessions read/write/delete files, but the route
// only requires nezha:server:write. PAT scopes are advertised as fine-grained
// (read / write / delete / exec); allowing a write-only PAT to open an FM
// session that can list & remove files silently widens the scope.
func TestRestScopeAllOf_RequiresEveryScope(t *testing.T) {
	mw := restScopeAllOf(model.ScopeServerRead, model.ScopeServerWrite, model.ScopeServerDelete)

	t.Run("rejects_token_missing_delete", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		tok := &model.APIToken{ScopesCSV: "nezha:server:read,nezha:server:write"}
		c.Set(apiTokenCtxKey, tok)
		c.Set(model.CtxKeyAPIToken, tok)
		mw(c)
		if !c.IsAborted() || w.Code != 403 {
			t.Fatalf("missing delete scope must abort with 403, got aborted=%v code=%d", c.IsAborted(), w.Code)
		}
	})

	t.Run("accepts_token_with_all_scopes", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		tok := &model.APIToken{ScopesCSV: "nezha:server:read,nezha:server:write,nezha:server:delete"}
		c.Set(apiTokenCtxKey, tok)
		c.Set(model.CtxKeyAPIToken, tok)
		mw(c)
		if c.IsAborted() {
			t.Fatal("token carrying all required scopes must pass")
		}
	})

	t.Run("jwt_callers_skip_check", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		mw(c)
		if c.IsAborted() {
			t.Fatal("JWT (no PAT) must pass through restScopeAllOf unchanged")
		}
	})

	t.Run("wildcard_resource_scope_satisfies_all", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		tok := &model.APIToken{ScopesCSV: "nezha:server:*"}
		c.Set(apiTokenCtxKey, tok)
		c.Set(model.CtxKeyAPIToken, tok)
		mw(c)
		if c.IsAborted() {
			t.Fatal("nezha:server:* must satisfy server:read+write+delete")
		}
	})
}
