package controller

import (
	"errors"
	"log"
	"strings"

	jwt "github.com/golang-jwt/jwt/v4"
	"gorm.io/gorm"

	ginjwt "github.com/appleboy/gin-jwt/v2"
	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// dashboardCredentialJWTParser verifies that a JWT was signed by this
// dashboard. It is derived from the auth middleware created in routers() and
// exists only for IsDashboardCredential; it must never authenticate dashboard
// requests on its own.
var dashboardCredentialJWTParser *ginjwt.GinJWTMiddleware

// newDashboardCredentialJWTParser derives a signature-only parser from the
// dashboard auth middleware. It keeps the exact key value and the pinned HS256
// signing algorithm of initParams() — there is no second read of
// singleton.Conf.JWTSecretKey — and disables claims validation so expiry does
// not change the verdict: an expired token is still proof the dashboard issued
// it, and keeping expired dashboard tokens out of the NAT tunnel prevents a
// careless backend from treating "any JWT" as its own credential. The live
// auth middleware keeps full claims validation; dashboard authentication is
// not relaxed by this parser in any way.
func newDashboardCredentialJWTParser(authMiddleware *ginjwt.GinJWTMiddleware) *ginjwt.GinJWTMiddleware {
	parser := *authMiddleware
	parser.ParseOptions = []jwt.ParserOption{jwt.WithoutClaimsValidation()}
	return &parser
}

// IsDashboardCredential reports whether an Authorization header value was
// issued by this dashboard: a panel-signed JWT or a panel API token (PAT).
//
// NAT ingress uses this to enforce the tunnel credential invariant: values
// classified as dashboard-issued are stripped before the request is tunneled
// to the agent, while foreign credentials are forwarded untouched because the
// NAT backend may authenticate with credentials of its own.
//
// Decision policy mirrors the dashboard's own auth middlewares: only the
// case-sensitive "Bearer " scheme is considered; the value after the scheme is
// classified by IsDashboardCredentialValue, which is also the entry point for
// the panel's scheme-less TokenLookup channels. Only a deterministic proof
// that the value was not issued by this dashboard (scheme mismatch, unknown
// PAT hash, failed signature verification) returns false.
func IsDashboardCredential(authz string) bool {
	raw := strings.TrimSpace(authz)
	if !strings.HasPrefix(raw, "Bearer ") {
		return false
	}
	return IsDashboardCredentialValue(strings.TrimPrefix(raw, "Bearer "))
}

// IsDashboardCredentialValue reports whether a raw credential value — the part
// after the "Bearer " scheme, exactly as the panel's other TokenLookup
// channels carry it (the ?token= query parameter and the nz-jwt cookie, see
// initParams) — was issued by this dashboard: a panel-signed JWT or a panel
// API token (PAT).
//
// Failure policy is identical to IsDashboardCredential: whenever the
// classification cannot be completed (parser or config not ready, database
// failure) the value is conservatively reported as a dashboard credential so
// the caller strips it; only a deterministic proof that the value was not
// issued by this dashboard (unknown PAT hash, failed signature verification)
// returns false.
func IsDashboardCredentialValue(value string) bool {
	plaintext := strings.TrimSpace(value)
	if strings.HasPrefix(plaintext, model.APITokenPrefix) {
		return isDashboardAPIToken(plaintext)
	}
	return isDashboardSignedJWT(plaintext)
}

// isDashboardAPIToken resolves a PAT-shaped value against the api_tokens
// table. The plaintext token is only ever hashed here; it is never logged.
func isDashboardAPIToken(plaintext string) bool {
	if singleton.DB == nil {
		logDashboardCredentialUnclassifiable("api token lookup unavailable")
		return true
	}
	var tok model.APIToken
	err := singleton.DB.Where("token_hash = ?", model.HashAPIToken(plaintext)).First(&tok).Error
	if err == nil {
		return true
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Deterministic proof the token was not issued by this dashboard:
		// forwarding it leaks nothing the dashboard ever validated.
		return false
	}
	logDashboardCredentialUnclassifiable("api token lookup failed")
	return true
}

// isDashboardSignedJWT verifies the token signature with the dashboard's
// pinned algorithm and key. Claims validation is disabled on purpose (see
// newDashboardCredentialJWTParser): expiry does not change who issued the
// token.
func isDashboardSignedJWT(token string) bool {
	if dashboardCredentialJWTParser == nil {
		// routers() has not run yet (startup incomplete): fail closed.
		logDashboardCredentialUnclassifiable("dashboard credential parser unavailable")
		return true
	}
	_, err := dashboardCredentialJWTParser.ParseTokenString(token)
	return err == nil
}

// logDashboardCredentialUnclassifiable records a classification failure. The
// Authorization value itself is never included in the message.
func logDashboardCredentialUnclassifiable(reason string) {
	log.Printf("NEZHA>> NAT ingress: Authorization value could not be classified (%s); stripping it as a precaution", reason)
}
