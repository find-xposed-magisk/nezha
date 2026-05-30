package controller

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
)

// setCSRFCookie issues a fresh CSRF token cookie. Called by login + refresh
// handlers so the frontend always has a paired value to mirror back into
// the X-CSRF-Token header. The cookie is intentionally HttpOnly=false —
// SPA JS must be able to read it. SameSite=Strict here (not Lax) because
// the cookie's sole purpose is the same-origin double-submit check and we
// don't want it leaking on cross-site GET navigation either.
func setCSRFCookie(c *gin.Context) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(csrfCookieName, hex.EncodeToString(b[:]), 0, "/", "", false, false)
}

const (
	csrfCookieName = "nz-csrf"
	csrfHeaderName = "X-CSRF-Token"
)

// csrfMiddleware enforces a double-submit-cookie CSRF gate on unsafe
// HTTP methods for cookie-authenticated requests.
//
// Why: SameSite=Lax on the nz-jwt cookie blocks the simplest cross-site
// form POST, but it does not stop same-site XSS-pivot CSRF, header method
// override, redirect-leaking auth helpers, or carefully chained sub-domain
// attacks. The double-submit pattern (server sets a JS-readable nz-csrf
// cookie, client mirrors the value into X-CSRF-Token) closes the gap
// without coupling auth state to a server-side session.
//
// Bypass conditions:
//   - Safe methods (GET/HEAD/OPTIONS): no state mutation, no CSRF risk.
//   - Bearer-token PAT requests (`Authorization: Bearer nzp_*`): stateless,
//     no ambient cookie, so a CSRF attack cannot induce them.
//
// Reject conditions:
//   - Missing or empty X-CSRF-Token header.
//   - Missing or empty nz-csrf cookie.
//   - Header value != cookie value.
//
// The middleware DOES NOT set the csrf cookie on its own — that is the
// JWT login / refresh handler's job, since those are the only places that
// know when to mint a fresh value. The pair just has to exist by the time
// any unsafe call reaches here.
func csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			// Self-heal sessions that predate the CSRF cookie (or whose
			// nz-csrf expired): seed a fresh value on a safe method so the
			// double-submit pair exists before the next unsafe call. Safe
			// methods mutate nothing, so minting here carries no CSRF risk.
			if cookie, err := c.Cookie(csrfCookieName); err != nil || cookie == "" {
				setCSRFCookie(c)
			}
			c.Next()
			return
		}
		// A PAT request carries no ambient cookie, so CSRF cannot induce it.
		// The exemption must check the authenticated PAT identity resolved by
		// apiTokenAuthMiddleware, not a forgeable Authorization header value.
		if APITokenFromContext(c) != nil {
			c.Next()
			return
		}
		header := c.GetHeader(csrfHeaderName)
		cookie, err := c.Cookie(csrfCookieName)
		if err != nil || cookie == "" || header == "" || header != cookie {
			c.AbortWithStatusJSON(http.StatusForbidden, model.CommonResponse[any]{
				Success: false,
				Error:   "ApiErrorForbidden: missing or invalid CSRF token",
			})
			return
		}
		c.Next()
	}
}
