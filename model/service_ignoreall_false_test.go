package model

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func adminPATCtx(tok *APIToken) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(CtxKeyAuthorizedUser, &User{Common: Common{ID: 1}, Role: RoleAdmin})
	c.Set(CtxKeyAPIToken, tok)
	return c
}

// ServiceCoverIgnoreAll permission must only consider SkipServers entries
// whose value is true (the allow-set actually dispatched at runtime). A
// `{2: false}` entry has no dispatch effect, so a PAT scoped to {1} must
// still be allowed to manage this service.
func TestServiceHasPermissionIgnoreAllSkipsFalseEntries(t *testing.T) {
	tok := &APIToken{ID: 1, UserID: 1}
	tok.SetServerIDs([]uint64{1})

	svc := &Service{
		Common:      Common{ID: 10, UserID: 1},
		Cover:       ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{1: true, 2: false},
	}

	if !svc.HasPermission(adminPATCtx(tok)) {
		t.Fatal("a `{2: false}` allow-set entry must not block a PAT scoped to {1}")
	}
}

func TestServiceHasPermissionIgnoreAllRejectsForeignTrueEntry(t *testing.T) {
	tok := &APIToken{ID: 1, UserID: 1}
	tok.SetServerIDs([]uint64{1})

	svc := &Service{
		Common:      Common{ID: 10, UserID: 1},
		Cover:       ServiceCoverIgnoreAll,
		SkipServers: map[uint64]bool{2: true},
	}

	if svc.HasPermission(adminPATCtx(tok)) {
		t.Fatal("a true allow-set entry on server 2 must reject a PAT scoped to {1}")
	}
}
