//go:build !agentcompat

package rpc

import (
	"net/http"
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
		"dashboard jwt":         natTestDashboardJWT,
		"expired dashboard jwt": natTestDashboardExpiredJWT,
		"dashboard api token":   natTestDashboardAPIToken,
	} {
		t.Run(name, func(t *testing.T) {
			request := &http.Request{Header: make(http.Header)}
			request.Header.Set("Authorization", credential)

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
				t.Fatalf("default NAT capability hook retained dashboard credential %q", got)
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
