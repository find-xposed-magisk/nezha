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
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, "ordinary-data")
	request.Header.Set("Authorization", "Bearer deterministic-secret")

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
	if got := request.Header.Get("Authorization"); got != "" {
		t.Fatalf("default NAT capability hook retained Authorization %q", got)
	}
}
