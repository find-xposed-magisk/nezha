package rpc

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	natDashboardCredentialGateMu.RLock()
	original := natDashboardCredentialValueGate
	natDashboardCredentialGateMu.RUnlock()
	SetNATDashboardCredentialGate(func(_ context.Context, values []string) []bool {
		results := make([]bool, len(values))
		for i, value := range values {
			switch value {
			case natTestDashboardJWTValue, natTestDashboardExpiredJWTValue, natTestDashboardPATValue:
				results[i] = true
			}
		}
		return results
	})
	t.Cleanup(func() {
		SetNATDashboardCredentialGate(original)
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

func TestStripDashboardCredentialsCookieChannelEscapedValue(t *testing.T) {
	// Gin's cookie TokenLookup URL-decodes the value before JWT validation.
	// NAT must classify the same decoded value or an encoded dashboard JWT can
	// be accepted by the panel and then leak unchanged to the backend.
	installNATCredentialTestGate(t)
	request := &http.Request{Header: make(http.Header)}
	escaped := strings.ReplaceAll(natTestDashboardJWTValue, "-", "%2D")
	request.Header.Set("Cookie", "nz-jwt="+escaped+"; sid=abc")

	stripDashboardCredentials(request)

	if got := request.Header.Get("Cookie"); got != "sid=abc" {
		t.Fatalf("cookie channel kept %q, want escaped dashboard token dropped", got)
	}
}

func TestStripDashboardCredentialsDeduplicatesAndBoundsClassification(t *testing.T) {
	natDashboardCredentialGateMu.RLock()
	original := natDashboardCredentialValueGate
	natDashboardCredentialGateMu.RUnlock()
	t.Cleanup(func() { SetNATDashboardCredentialGate(original) })

	classifiedValues := 0
	gateCalls := 0
	SetNATDashboardCredentialGate(func(_ context.Context, values []string) []bool {
		gateCalls++
		classifiedValues += len(values)
		return make([]bool, len(values))
	})

	query := make(url.Values)
	for range 100 {
		query.Add("token", model.APITokenPrefix+"same-value")
	}
	for i := range 100 {
		query.Add("token", fmt.Sprintf("%sunique-%d", model.APITokenPrefix, i))
	}
	request := &http.Request{Header: make(http.Header), URL: &url.URL{RawQuery: query.Encode()}}

	stripDashboardCredentials(request)

	if gateCalls != 1 {
		t.Fatalf("classifier called %d times, want one batched call", gateCalls)
	}
	if classifiedValues > maxNATDashboardCredentialValues {
		t.Fatalf("classified %d credential values, want at most %d per request", classifiedValues, maxNATDashboardCredentialValues)
	}
	if strings.Contains(request.URL.RawQuery, model.APITokenPrefix+"unique-99") {
		t.Fatal("credential beyond the classification budget survived; overflow must fail closed")
	}
}

func TestPrepareNATCapabilityFailsClosedWithoutGate(t *testing.T) {
	// Given — unwired gates: every value on every channel is treated as
	// dashboard-issued and stripped, foreign ones included.
	SetNATDashboardCredentialGate(nil)
	t.Cleanup(func() { SetNATDashboardCredentialGate(nil) })
	request := &http.Request{
		Header: make(http.Header),
		URL:    &url.URL{Path: "/nat", RawQuery: "keep=1&token=" + natTestForeignJWTValue + "&token=%ZZ"},
	}
	request.Header.Set("Authorization", natTestForeignRandom)
	request.Header.Set("Cookie", "sid=abc; nz-jwt="+natTestForeignJWTValue+"; nz-jwt=%ZZ")

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
