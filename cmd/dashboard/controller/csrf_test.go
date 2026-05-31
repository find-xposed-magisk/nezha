package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

// H6 regression: cookie-JWT unsafe-method routes need a real CSRF gate.
// SameSite=Lax blocks the obvious cross-site form-POST but does not stop
// same-site siblings, sub-domain XSS pivots, header method override quirks,
// or the various legacy edge cases. The middleware below is the double
// -submit cookie pattern: require X-CSRF-Token whose value matches the
// `nz-csrf` cookie. PAT bearer requests bypass the gate (they don't carry
// the cookie at all and are already authenticated stateless).
func TestCSRFMiddleware_AllowsSafeMethodsWithoutToken(t *testing.T) {
	mw := csrfMiddleware()
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		t.Run(m, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(m, "/api/v1/profile", nil)
			mw(c)
			if c.IsAborted() {
				t.Fatalf("%s must pass without csrf token", m)
			}
		})
	}
}

// Self-heal: a session created before the CSRF cookie existed (or one whose
// nz-csrf expired) only carries nz-jwt. Without seeding a fresh nz-csrf on a
// safe GET, every subsequent unsafe call — including the auto refresh-token
// POST — would 403 forever and force a manual re-login. GET carries no CSRF
// risk, so the middleware mints the cookie when it is absent.
func TestCSRFMiddleware_SeedsCookieOnSafeMethodWhenMissing(t *testing.T) {
	withCSRFSecret(t, "test-jwt-secret")
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: "nz-jwt", Value: "session"})
	mw(c)
	if c.IsAborted() {
		t.Fatal("safe GET must never abort")
	}
	var seeded bool
	for _, sc := range w.Result().Cookies() {
		if sc.Name == csrfCookieName && sc.Value != "" {
			seeded = true
		}
	}
	if !seeded {
		t.Fatal("missing nz-csrf must be seeded on a safe GET so the SPA can self-heal")
	}
}

func TestCSRFMiddleware_DoesNotReseedWhenCookiePresent(t *testing.T) {
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "existing"})
	mw(c)
	for _, sc := range w.Result().Cookies() {
		if sc.Name == csrfCookieName {
			t.Fatal("existing nz-csrf must not be rotated on every GET")
		}
	}
}

func TestCSRFMiddleware_BlocksUnsafeMethodWithoutToken(t *testing.T) {
	mw := csrfMiddleware()
	for _, m := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		t.Run(m, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(m, "/api/v1/profile", nil)
			c.Request.AddCookie(&http.Cookie{Name: "nz-jwt", Value: "anything"})
			mw(c)
			if !c.IsAborted() || w.Code != http.StatusForbidden {
				t.Fatalf("%s without csrf token must abort 403, got aborted=%v code=%d", m, c.IsAborted(), w.Code)
			}
		})
	}
}

func TestCSRFMiddleware_AcceptsMatchingHeaderAndCookie(t *testing.T) {
	withCSRFSecret(t, "test-jwt-secret")
	token := issueCSRFToken()
	mw := csrfMiddleware()
	for _, m := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		t.Run(m, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(m, "/api/v1/profile", nil)
			c.Request.AddCookie(&http.Cookie{Name: "nz-jwt", Value: "anything"})
			c.Request.AddCookie(&http.Cookie{Name: "nz-csrf", Value: token})
			c.Request.Header.Set("X-CSRF-Token", token)
			mw(c)
			if c.IsAborted() {
				t.Fatalf("%s with matching signed csrf header+cookie must pass", m)
			}
		})
	}
}

func TestCSRFMiddleware_RejectsMismatchedHeader(t *testing.T) {
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: "nz-csrf", Value: "value-a"})
	c.Request.Header.Set("X-CSRF-Token", "value-b")
	mw(c)
	if !c.IsAborted() || w.Code != http.StatusForbidden {
		t.Fatalf("mismatched csrf token must abort 403, got aborted=%v code=%d", c.IsAborted(), w.Code)
	}
}

func TestCSRFMiddleware_AuthenticatedPATBypassesCheck(t *testing.T) {
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
	c.Set(apiTokenCtxKey, &model.APIToken{ID: 1})
	mw(c)
	if c.IsAborted() {
		t.Fatal("authenticated PAT must bypass CSRF — stateless auth")
	}
}

func TestCSRFMiddleware_ForgedBearerHeaderDoesNotBypass(t *testing.T) {
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: "nz-jwt", Value: "session"})
	c.Request.Header.Set("Authorization", "Bearer "+model.APITokenPrefix+"never-authenticated")
	mw(c)
	if !c.IsAborted() || w.Code != http.StatusForbidden {
		t.Fatalf("a Bearer nzp_* header that never authenticated must not skip CSRF, got aborted=%v code=%d", c.IsAborted(), w.Code)
	}
}

func TestCSRFMiddleware_EmptyTokenRejected(t *testing.T) {
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: "nz-csrf", Value: ""})
	c.Request.Header.Set("X-CSRF-Token", "")
	mw(c)
	if !c.IsAborted() {
		t.Fatal("empty csrf token must not satisfy the check (defeats the gate entirely)")
	}
}
