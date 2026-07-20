package evidence

import (
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestEvidence_DedicatedArtifactsRequireExactScenarioAssertions(t *testing.T) {
	tests := []struct {
		name      string
		scenario  string
		assertion string
		artifact  func(*testing.T, string)
	}{
		{"transfer generic", contract.ScenarioTransfer100MiB, "scenario passed", func(t *testing.T, dir string) {
			writeJSONEvidenceFile(t, dir, "transfer.json", validTransferArtifact("", true))
		}},
		{"transfer mismatched", contract.ScenarioTransfer100MiB, contract.AssertionReconnectDisconnect, func(t *testing.T, dir string) {
			writeJSONEvidenceFile(t, dir, "transfer.json", validTransferArtifact("", true))
		}},
		{"reconnect generic", contract.ScenarioReconnect, "scenario passed", func(t *testing.T, dir string) {
			writeJSONEvidenceFile(t, dir, "reconnect.json", validReconnectArtifact(t, "", true))
		}},
		{"reconnect mismatched", contract.ScenarioReconnect, contract.AssertionTransferWarmup, func(t *testing.T, dir string) {
			writeJSONEvidenceFile(t, dir, "reconnect.json", validReconnectArtifact(t, "", true))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDedicatedExecutableEvidence(t, dir, test.scenario, "", true)
			results := Results{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: test.scenario, Passed: true, Assertions: []Assertion{{Name: test.assertion, Passed: true}}}}}
			writeJSONEvidenceFile(t, dir, "results.json", results)
			junit, err := JUnit(results)
			if err != nil {
				t.Fatalf("JUnit: %v", err)
			}
			writeEvidenceFile(t, dir, "junit.xml", string(junit))
			test.artifact(t, dir)
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("generic assertion accepted for dedicated evidence")
			}
		})
	}
}
