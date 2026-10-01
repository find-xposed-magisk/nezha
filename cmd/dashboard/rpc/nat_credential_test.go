package rpc

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/nezhahq/nezha/model"
)

// Values standing in for the credentials this dashboard issues. The fake
// gates below mirror the contract of controller.IsDashboardCredential and
// controller.IsDashboardCredentialValue; the real classification semantics —
// panel JWTs accepted regardless of expiry, foreign-signed JWTs and random
// strings rejected — are pinned by the controller package tests
// (dashboard_credential_test.go) and by the flow test that wires the real
// classifiers into ServeNAT (dashboard_credential_nat_flow_test.go).
const (
	natTestDashboardJWTValue        = "dashboard-signed-jwt"
	natTestDashboardExpiredJWTValue = "dashboard-signed-expired-jwt"
	natTestDashboardPATValue        = model.APITokenPrefix + "dashboard-issued-pat"
	natTestForeignJWTValue          = "foreign-signed-jwt"
	natTestForeignRandomValue       = "foreign-random-string"

	natTestDashboardJWT        = "Bearer " + natTestDashboardJWTValue
	natTestDashboardExpiredJWT = "Bearer " + natTestDashboardExpiredJWTValue
	natTestDashboardAPIToken   = "Bearer " + natTestDashboardPATValue
	natTestForeignJWT          = "Bearer " + natTestForeignJWTValue
	natTestForeignRandom       = "Bearer " + natTestForeignRandomValue
)

// installNATCredentialTestGate wires fake dashboard-credential classifiers
// for the duration of the test: exactly the dashboard-issued test values above
// are classified as dashboard credentials on every channel, everything else
// is foreign.
func installNATCredentialTestGate(t *testing.T) {
	t.Helper()
	originalAuthorization := natDashboardAuthorizationGate
	originalValue := natDashboardCredentialValueGate
	natDashboardAuthorizationGate = func(authz string) bool {
		switch authz {
		case natTestDashboardJWT, natTestDashboardExpiredJWT, natTestDashboardAPIToken:
			return true
		default:
			return false
		}
	}
	natDashboardCredentialValueGate = func(value string) bool {
		switch value {
		case natTestDashboardJWTValue, natTestDashboardExpiredJWTValue, natTestDashboardPATValue:
			return true
		default:
			return false
		}
	}
	t.Cleanup(func() {
		natDashboardAuthorizationGate = originalAuthorization
		natDashboardCredentialValueGate = originalValue
	})
}

func TestStripDashboardCredentialsQueryChannel(t *testing.T) {
	// Given
	installNATCredentialTestGate(t)
	request := &http.Request{URL: &url.URL{Path: "/nat", RawQuery: "keep=1&token=" + natTestDashboardJWTValue + "&token=" + natTestForeignJWTValue + "&a=%2Fx"}}

	// When
	stripDashboardCredentials(request)

	// Then
	if got := request.URL.RawQuery; got != "keep=1&token="+natTestForeignJWTValue+"&a=%2Fx" {
		t.Fatalf("query channel kept %q, want dashboard token dropped and foreign values byte-preserved", got)
	}
}

func TestStripDashboardCredentialsQueryChannelEscapedValue(t *testing.T) {
	// Given — a dashboard token percent-encoded in the query must still be
	// recognized, while the escaped foreign element stays verbatim.
	installNATCredentialTestGate(t)
	request := &http.Request{URL: &url.URL{Path: "/nat", RawQuery: "token=" + url.QueryEscape(natTestDashboardJWTValue) + "&token=" + url.QueryEscape(natTestForeignJWTValue)}}

	// When
	stripDashboardCredentials(request)

	// Then
	if got := request.URL.RawQuery; got != "token="+url.QueryEscape(natTestForeignJWTValue) {
		t.Fatalf("query channel kept %q, want escaped dashboard token dropped and foreign element verbatim", got)
	}
}

func TestStripDashboardCredentialsCookieChannel(t *testing.T) {
	// Given
	installNATCredentialTestGate(t)
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set("Cookie", "sid=abc; nz-jwt="+natTestDashboardJWTValue+"; nz-jwt="+natTestForeignJWTValue+"; theme=dark")

	// When
	stripDashboardCredentials(request)

	// Then
	if got := request.Header.Get("Cookie"); got != "sid=abc; nz-jwt="+natTestForeignJWTValue+"; theme=dark" {
		t.Fatalf("cookie channel kept %q, want dashboard pair dropped and foreign pairs verbatim", got)
	}
}

func TestStripDashboardCredentialsCookieChannelQuotedValue(t *testing.T) {
	// Given — quoting is presentation the panel's cookie parsing strips before
	// verifying, so a quoted dashboard token must be recognized.
	installNATCredentialTestGate(t)
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set("Cookie", `nz-jwt="`+natTestDashboardJWTValue+`"; sid=abc`)

	// When
	stripDashboardCredentials(request)

	// Then
	if got := request.Header.Get("Cookie"); got != "sid=abc" {
		t.Fatalf("cookie channel kept %q, want quoted dashboard token dropped", got)
	}
}

func TestPrepareNATCapabilityFailsClosedWithoutGate(t *testing.T) {
	// Given — unwired gates: every value on every channel is treated as
	// dashboard-issued and stripped, foreign ones included.
	SetNATDashboardCredentialGate(nil, nil)
	t.Cleanup(func() { SetNATDashboardCredentialGate(nil, nil) })
	request := &http.Request{
		Header: make(http.Header),
		URL:    &url.URL{Path: "/nat", RawQuery: "keep=1&token=" + natTestForeignJWTValue},
	}
	request.Header.Set("Authorization", natTestForeignRandom)
	request.Header.Set("Cookie", "sid=abc; nz-jwt="+natTestForeignJWTValue)

	// When
	lease, err := prepareNATCapability(request, &model.NAT{Common: model.Common{ID: 91}, ServerID: 81})

	// Then
	if err != nil {
		t.Fatalf("fail-closed NAT capability hook returned error: %v", err)
	}
	if lease.active {
		t.Fatal("fail-closed NAT capability hook unexpectedly activated")
	}
	if got := request.Header.Get("Authorization"); got != "" {
		t.Fatalf("fail-closed hook retained Authorization %q", got)
	}
	if got := request.URL.RawQuery; got != "keep=1" {
		t.Fatalf("fail-closed hook kept query %q, want token parameter stripped", got)
	}
	if got := request.Header.Get("Cookie"); got != "sid=abc" {
		t.Fatalf("fail-closed hook kept cookie %q, want nz-jwt pair stripped", got)
	}
}
