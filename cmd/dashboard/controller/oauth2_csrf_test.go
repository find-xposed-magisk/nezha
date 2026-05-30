package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// setCSRFCookie must mint a readable nz-csrf cookie; OAuth2 callback relies on
// it so OAuth-only sessions can satisfy the double-submit CSRF gate.
func TestSetCSRFCookieIssuesReadableToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	setCSRFCookie(c)

	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, csrfCookieName+"=") {
		t.Fatalf("expected %s cookie, got %q", csrfCookieName, setCookie)
	}
	if strings.Contains(strings.ToLower(setCookie), "httponly") {
		t.Fatal("CSRF cookie must be JS-readable (not HttpOnly) for the SPA to mirror it")
	}
}
