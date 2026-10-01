package main

import (
	"testing"

	"github.com/nezhahq/nezha/cmd/dashboard/rpc"
)

// TestWireNATDashboardCredentialGate pins the NAT ingress wiring contract:
// main() must connect the real batched dashboard-credential classifier
// (controller.ClassifyDashboardCredentialValues)
// through wireNATDashboardCredentialGate before any listener serves traffic.
// That the wired functions really classify per channel is pinned end to end
// by the controller package's real-classifier NAT flow test.
func TestWireNATDashboardCredentialGate(t *testing.T) {
	if rpc.NATDashboardCredentialGateWired() {
		t.Fatal("NAT dashboard credential gate already wired before the test")
	}

	wireNATDashboardCredentialGate()

	if !rpc.NATDashboardCredentialGateWired() {
		t.Fatal("wireNATDashboardCredentialGate left the NAT dashboard credential gate unwired")
	}
	// Leave the process in the wired state main() establishes.
	t.Cleanup(wireNATDashboardCredentialGate)
}
