package agentcompatcontract

import "testing"

func TestIOStreamCapabilityHeaderUsesFrozenName(t *testing.T) {
	if IOStreamCapabilityHeader != "X-Nezha-AgentCompat-IOStream-Capability" {
		t.Fatalf("unexpected capability header name")
	}
}
