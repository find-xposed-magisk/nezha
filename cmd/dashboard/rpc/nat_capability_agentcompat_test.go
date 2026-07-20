//go:build agentcompat

package rpc

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	serviceRPC "github.com/nezhahq/nezha/service/rpc"
)

func TestPrepareNATCapabilityConsumesAndRemovesHeaderBeforeNATWork(t *testing.T) {
	// Given
	handler := serviceRPC.NewNezhaHandler()
	original := serviceRPC.NezhaHandlerSingleton
	serviceRPC.NezhaHandlerSingleton = handler
	t.Cleanup(func() { serviceRPC.NezhaHandlerSingleton = original })
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, "malformed")

	// When
	lease, err := prepareNATCapability(request, &model.NAT{Common: model.Common{ID: 91}, ServerID: 81})

	// Then
	if err == nil {
		t.Fatal("malformed capability unexpectedly accepted")
	}
	if lease.active {
		t.Fatal("malformed capability unexpectedly activated")
	}
	if _, present := request.Header[agentcompatcontract.IOStreamCapabilityHeader]; present {
		t.Fatal("capability header remained after hook")
	}
}

func TestPrepareNATCapabilityRejectsDuplicateHeaderAfterRemovingAllValues(t *testing.T) {
	// Given
	request := &http.Request{Header: make(http.Header)}
	request.Header.Add(agentcompatcontract.IOStreamCapabilityHeader, "one")
	request.Header.Add(agentcompatcontract.IOStreamCapabilityHeader, "two")

	// When
	lease, err := prepareNATCapability(request, &model.NAT{Common: model.Common{ID: 92}, ServerID: 82})

	// Then
	if err == nil {
		t.Fatal("duplicate capability header unexpectedly accepted")
	}
	if lease.active {
		t.Fatal("duplicate capability header unexpectedly activated")
	}
	if _, present := request.Header[agentcompatcontract.IOStreamCapabilityHeader]; present {
		t.Fatal("duplicate capability header remained after hook")
	}
}

func TestServeNATAgentCompatSensitiveHeadersStayOutOfErrorsAndLogs(t *testing.T) {
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, "capability-secret")
	request.Header.Set("Authorization", "Bearer deterministic-secret")
	writer := &serveNATResponseWriter{}
	var logs bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(originalOutput) })

	ServeNAT(writer, request, &model.NAT{Common: model.Common{ID: 91}, ServerID: 81})

	if writer.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", writer.status, http.StatusServiceUnavailable)
	}
	for _, output := range []string{writer.body, logs.String()} {
		if strings.Contains(output, "capability-secret") || strings.Contains(output, "deterministic-secret") {
			t.Fatalf("sensitive value leaked in %q", output)
		}
	}
}
