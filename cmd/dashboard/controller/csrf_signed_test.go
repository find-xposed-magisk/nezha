package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func withCSRFSecret(t *testing.T, secret string) {
	t.Helper()
	prev := singleton.Conf
	singleton.Conf = &singleton.ConfigClass{Config: &model.Config{}}
	singleton.Conf.JWTSecretKey = secret
	t.Cleanup(func() { singleton.Conf = prev })
}

// Sibling-subdomain cookie tossing: an attacker who can set nz-csrf for the
// parent domain injects an attacker-chosen value and mirrors it into the
// header. A naive double-submit accepts header==cookie. The signed
// double-submit must reject it because the injected value carries no valid
// server HMAC.
func TestCSRFMiddleware_RejectsUnsignedInjectedPair(t *testing.T) {
	withCSRFSecret(t, "test-jwt-secret")
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: "nz-jwt", Value: "session"})
	c.Request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "attacker-chosen"})
	c.Request.Header.Set(csrfHeaderName, "attacker-chosen")
	mw(c)
	if !c.IsAborted() || w.Code != http.StatusForbidden {
		t.Fatalf("an unsigned (cookie-tossed) csrf pair must be rejected, got aborted=%v code=%d", c.IsAborted(), w.Code)
	}
}

// A token minted by the server (issueCSRFToken) must pass when mirrored
// correctly into the header.
func TestCSRFMiddleware_AcceptsServerSignedPair(t *testing.T) {
	withCSRFSecret(t, "test-jwt-secret")
	token := issueCSRFToken()
	if token == "" {
		t.Fatal("issueCSRFToken must produce a value")
	}
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: "nz-jwt", Value: "session"})
	c.Request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	c.Request.Header.Set(csrfHeaderName, token)
	mw(c)
	if c.IsAborted() {
		t.Fatal("a correctly mirrored server-signed csrf pair must pass")
	}
}

// Even with header==cookie, a value whose signature does not verify under the
// server secret must be rejected (forged signature segment).
func TestCSRFMiddleware_RejectsForgedSignature(t *testing.T) {
	withCSRFSecret(t, "test-jwt-secret")
	token := issueCSRFToken()
	forged := token[:len(token)-1]
	if token[len(token)-1] == 'a' {
		forged += "b"
	} else {
		forged += "a"
	}
	mw := csrfMiddleware()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/profile", nil)
	c.Request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: forged})
	c.Request.Header.Set(csrfHeaderName, forged)
	mw(c)
	if !c.IsAborted() || w.Code != http.StatusForbidden {
		t.Fatalf("a forged-signature csrf pair must be rejected, got aborted=%v code=%d", c.IsAborted(), w.Code)
	}
}
