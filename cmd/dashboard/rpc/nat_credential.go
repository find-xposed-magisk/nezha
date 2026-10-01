package rpc

import (
	"net/http"
	"net/url"
	"strings"
)

// Channel names of the panel's own TokenLookup (controller/jwt.go
// "header: Authorization, query: token, cookie: nz-jwt"). A dashboard JWT
// can therefore reach the NAT ingress through the Authorization header, the
// ?token= query parameter or the nz-jwt cookie, and all three must be
// classified before the request is tunneled to the agent.
const (
	natDashboardJWTCookieName     = "nz-jwt"
	natDashboardJWTQueryParameter = "token"
)

var (
	// natDashboardAuthorizationGate classifies an Authorization header value
	// (case-sensitive "Bearer " scheme included) as a credential issued by
	// this dashboard.
	natDashboardAuthorizationGate func(authz string) bool
	// natDashboardCredentialValueGate classifies a raw credential value — the
	// part after the "Bearer " scheme, exactly as the ?token= query parameter
	// and the nz-jwt cookie carry it — as a credential issued by this
	// dashboard.
	natDashboardCredentialValueGate func(value string) bool
)

// SetNATDashboardCredentialGate wires the dashboard-credential classifiers
// used by the NAT ingress. main() injects
// controller.IsDashboardCredential and controller.IsDashboardCredentialValue;
// the setter keeps the rpc package free of a dependency on the controller
// package, the same pattern as SetReceiptGateListener and
// SetMCPKillSwitchObserver. A nil function fails closed: every value on that
// channel is then treated as dashboard-issued and stripped.
func SetNATDashboardCredentialGate(authorizationClass func(authz string) bool, valueClass func(value string) bool) {
	natDashboardAuthorizationGate = authorizationClass
	natDashboardCredentialValueGate = valueClass
}

// NATDashboardCredentialGateWired reports whether the dashboard-credential
// classifiers have been wired. It exists so the main() wiring contract can be
// pinned by a test.
func NATDashboardCredentialGateWired() bool {
	return natDashboardAuthorizationGate != nil && natDashboardCredentialValueGate != nil
}

// stripDashboardCredentials removes credentials issued by this dashboard from
// every channel the panel's own TokenLookup accepts: the Authorization
// header, the ?token= query parameter and the nz-jwt cookie. Such credentials
// authenticate the dashboard itself and must never become origin credentials
// for the NAT backend. Foreign values are forwarded untouched: the NAT
// backend may authenticate with credentials of its own, and this dashboard
// must not make policy for them.
func stripDashboardCredentials(request *http.Request) {
	stripDashboardAuthorizationHeader(request)
	stripDashboardCredentialQuery(request)
	stripDashboardCredentialCookie(request)
}

// stripDashboardAuthorizationHeader removes the Authorization header when it
// carries a dashboard-issued credential.
func stripDashboardAuthorizationHeader(request *http.Request) {
	if natDashboardAuthorizationGate == nil {
		// Gate not wired (early startup or tests): fail closed and keep the
		// historical behavior of stripping every Authorization header.
		request.Header.Del("Authorization")
		return
	}
	// A multi-valued Authorization header is malformed traffic, but any
	// dashboard-issued value in it must still not survive into the tunnel, so
	// the whole header is dropped when one is recognized.
	for _, value := range request.Header.Values("Authorization") {
		if natDashboardAuthorizationGate(value) {
			request.Header.Del("Authorization")
			return
		}
	}
}

// stripDashboardCredentialQuery removes ?token= query parameters whose value
// is a dashboard-issued credential. Every other element of the query string —
// including foreign token values — is preserved byte-for-byte, so the
// forwarded request keeps the exact query the client sent.
func stripDashboardCredentialQuery(request *http.Request) {
	if request.URL == nil || request.URL.RawQuery == "" {
		return
	}
	elements := strings.Split(request.URL.RawQuery, "&")
	kept := elements[:0]
	changed := false
	for _, element := range elements {
		if isDashboardCredentialQueryElement(element) {
			changed = true
			continue
		}
		kept = append(kept, element)
	}
	if changed {
		request.URL.RawQuery = strings.Join(kept, "&")
	}
}

// isDashboardCredentialQueryElement reports whether a raw query element
// carries a dashboard-issued credential in the panel's token parameter.
func isDashboardCredentialQueryElement(element string) bool {
	rawName, rawValue, _ := strings.Cut(element, "=")
	// The panel reads the parameter by its decoded name (gin-jwt c.Query),
	// so an escaped key must be decoded before comparing.
	name, err := url.QueryUnescape(rawName)
	if err != nil || name != natDashboardJWTQueryParameter {
		return false
	}
	if natDashboardCredentialValueGate == nil {
		return true // fail closed
	}
	// A value that fails to decode is one the panel's own query parsing drops
	// as well, so it can never be a panel credential.
	decoded, err := url.QueryUnescape(rawValue)
	if err != nil {
		return false
	}
	return natDashboardCredentialValueGate(decoded)
}

// stripDashboardCredentialCookie removes nz-jwt cookies whose value is a
// dashboard-issued credential. All other cookie pairs — including foreign
// nz-jwt values — are preserved verbatim.
func stripDashboardCredentialCookie(request *http.Request) {
	lines := request.Header.Values("Cookie")
	if len(lines) == 0 {
		return
	}
	changed := false
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		nextLine, lineChanged := stripDashboardCredentialCookieLine(line)
		changed = changed || lineChanged
		if nextLine != "" {
			kept = append(kept, nextLine)
		}
	}
	if !changed {
		return
	}
	request.Header.Del("Cookie")
	for _, line := range kept {
		request.Header.Add("Cookie", line)
	}
}

// stripDashboardCredentialCookieLine rewrites a single Cookie header line,
// dropping the dashboard-issued nz-jwt pairs. Kept pairs keep their raw form,
// whitespace included — except that a pair following a dropped one no longer
// carries the removed pair's share of the "; " separator.
func stripDashboardCredentialCookieLine(line string) (string, bool) {
	pairs := strings.Split(line, ";")
	kept := pairs[:0]
	droppedBeforeFirstKept := true
	changed := false
	for _, pair := range pairs {
		name, value, _ := strings.Cut(pair, "=")
		if strings.TrimSpace(name) == natDashboardJWTCookieName &&
			(natDashboardCredentialValueGate == nil || isDashboardCredentialCookieValue(value)) {
			changed = true
			continue
		}
		if droppedBeforeFirstKept {
			pair = strings.TrimLeft(pair, " ")
		}
		droppedBeforeFirstKept = false
		kept = append(kept, pair)
	}
	if !changed {
		return line, false
	}
	return strings.Join(kept, ";"), true
}

// isDashboardCredentialCookieValue classifies a raw cookie value the same way
// the panel's cookie parsing would hand it to the JWT verifier: surrounding
// whitespace and quoting are presentation, not part of the credential.
func isDashboardCredentialCookieValue(rawValue string) bool {
	value := strings.Trim(strings.TrimSpace(rawValue), `"`)
	return natDashboardCredentialValueGate(value)
}
