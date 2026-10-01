//go:build !agentcompat

package rpc

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
)

func TestPrepareNATCapabilityDefaultPreservesHeaderAsOrdinaryRequestData(t *testing.T) {
	// Given
	installNATCredentialTestGate(t)
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, "ordinary-data")
	request.Header.Set("Authorization", natTestForeignRandom)

	// When
	lease, err := prepareNATCapability(request, &model.NAT{Common: model.Common{ID: 91}, ServerID: 81})

	// Then
	if err != nil {
		t.Fatalf("default NAT capability hook returned error: %v", err)
	}
	if lease.active {
		t.Fatal("default NAT capability hook unexpectedly activated")
	}
	if got := request.Header.Get(agentcompatcontract.IOStreamCapabilityHeader); got != "ordinary-data" {
		t.Fatalf("default NAT capability hook changed header to %q", got)
	}
	if got := request.Header.Get("Authorization"); got != natTestForeignRandom {
		t.Fatalf("default NAT capability hook changed foreign Authorization to %q", got)
	}
}

func TestPrepareNATCapabilityDefaultStripsDashboardCredentials(t *testing.T) {
	// Given
	installNATCredentialTestGate(t)
	for name, credential := range map[string]string{
		"dashboard jwt":         natTestDashboardJWTValue,
		"expired dashboard jwt": natTestDashboardExpiredJWTValue,
		"dashboard api token":   natTestDashboardPATValue,
	} {
		t.Run(name, func(t *testing.T) {
			request := &http.Request{
				Header: make(http.Header),
				URL:    &url.URL{Path: "/nat", RawQuery: "keep=1&token=" + credential},
			}
			request.Header.Set("Authorization", "Bearer "+credential)
			request.Header.Set("Cookie", "sid=abc; nz-jwt="+credential)

			// When
			lease, err := prepareNATCapability(request, &model.NAT{Common: model.Common{ID: 91}, ServerID: 81})

			// Then
			if err != nil {
				t.Fatalf("default NAT capability hook returned error: %v", err)
			}
			if lease.active {
				t.Fatal("default NAT capability hook unexpectedly activated")
			}
			if got := request.Header.Get("Authorization"); got != "" {
				t.Fatalf("default NAT capability hook retained dashboard credential in Authorization %q", got)
			}
			if got := request.URL.RawQuery; got != "keep=1" {
				t.Fatalf("default NAT capability hook retained dashboard credential in query %q", got)
			}
			if got := request.Header.Get("Cookie"); got != "sid=abc" {
				t.Fatalf("default NAT capability hook retained dashboard credential in cookie %q", got)
			}
		})
	}
}

func TestPrepareNATCapabilityDefaultKeepsForeignAndMissingAuthorization(t *testing.T) {
	// Given
	installNATCredentialTestGate(t)
	for name, values := range map[string][]string{
		"foreign jwt":    {natTestForeignJWT},
		"foreign random": {natTestForeignRandom},
		"empty value":    {""},
		"missing header": nil,
	} {
		t.Run(name, func(t *testing.T) {
			request := &http.Request{Header: make(http.Header)}
			for _, value := range values {
				request.Header.Set("Authorization", value)
			}

			// When
			lease, err := prepareNATCapability(request, &model.NAT{Common: model.Common{ID: 91}, ServerID: 81})

			// Then
			if err != nil {
				t.Fatalf("default NAT capability hook returned error: %v", err)
			}
			if lease.active {
				t.Fatal("default NAT capability hook unexpectedly activated")
			}
			got := request.Header.Values("Authorization")
			if len(got) != len(values) {
				t.Fatalf("default NAT capability hook changed Authorization to %q, want %q", got, values)
			}
			for i := range values {
				if got[i] != values[i] {
					t.Fatalf("default NAT capability hook changed Authorization to %q, want %q", got, values)
				}
			}
		})
	}
}

func TestPrepareNATCapabilityDefaultKeepsForeignAndMissingQueryAndCookie(t *testing.T) {
	// Given
	installNATCredentialTestGate(t)
	for name, tc := range map[string]struct{ rawQuery, cookie string }{
		"foreign values":  {"keep=1&token=" + natTestForeignJWTValue + "&a=%2Fx", "sid=abc; nz-jwt=" + natTestForeignJWTValue},
		"empty values":    {"keep=1&token=", "nz-jwt="},
		"missing channel": {"keep=1", "sid=abc"},
	} {
		t.Run(name, func(t *testing.T) {
			request := &http.Request{
				Header: make(http.Header),
				URL:    &url.URL{Path: "/nat", RawQuery: tc.rawQuery},
			}
			request.Header.Set("Cookie", tc.cookie)

			// When
			lease, err := prepareNATCapability(request, &model.NAT{Common: model.Common{ID: 91}, ServerID: 81})

			// Then
			if err != nil {
				t.Fatalf("default NAT capability hook returned error: %v", err)
			}
			if lease.active {
				t.Fatal("default NAT capability hook unexpectedly activated")
			}
			if got := request.URL.RawQuery; got != tc.rawQuery {
				t.Fatalf("default NAT capability hook changed query to %q, want %q", got, tc.rawQuery)
			}
			if got := request.Header.Get("Cookie"); got != tc.cookie {
				t.Fatalf("default NAT capability hook changed cookie to %q, want %q", got, tc.cookie)
			}
		})
	}
}
