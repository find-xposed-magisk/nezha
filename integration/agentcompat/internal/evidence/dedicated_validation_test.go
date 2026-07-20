package evidence

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestEvidence_TransferAndReconnectRequireDedicatedArtifacts(t *testing.T) {
	for _, scenarioName := range []string{"transfer-100mib", "reconnect"} {
		t.Run(scenarioName, func(t *testing.T) {
			dir := t.TempDir()
			writeExecutableEvidence(t, dir, "pr-full", scenarioName, true, true, true)
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("missing dedicated artifact accepted")
			}
		})
	}
}

func TestEvidence_ValidatesTypedTransferSuccessAndFaultArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		fault  string
		passed bool
	}{
		{"success", "", true},
		{"hash fault", "transfer-hash", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDedicatedExecutableEvidence(t, dir, "transfer-100mib", test.fault, test.passed)
			artifact := validTransferArtifact(test.fault, test.passed)
			writeJSONEvidenceFile(t, dir, "transfer.json", artifact)
			if err := ValidateDirectory(dir); err != nil {
				t.Fatalf("validate transfer evidence: %v", err)
			}
		})
	}
}

func TestEvidence_ValidatesTypedReconnectSuccessAndFaultArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		fault  string
		passed bool
	}{
		{"success", "", true},
		{"dashboard fault", "dashboard-exit", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDedicatedExecutableEvidence(t, dir, "reconnect", test.fault, test.passed)
			artifact := validReconnectArtifact(t, test.fault, test.passed)
			writeJSONEvidenceFile(t, dir, "reconnect.json", artifact)
			if err := ValidateDirectory(dir); err != nil {
				t.Fatalf("validate reconnect evidence: %v", err)
			}
		})
	}
}

func TestEvidence_RejectsMisleadingDedicatedArtifact(t *testing.T) {
	dir := t.TempDir()
	writeDedicatedExecutableEvidence(t, dir, "transfer-100mib", "transfer-hash", false)
	artifact := validTransferArtifact("transfer-hash", false)
	artifact.Evidence.UploadBytes = 104857600
	writeJSONEvidenceFile(t, dir, "transfer.json", artifact)
	if err := ValidateDirectory(dir); err == nil {
		t.Fatal("fault artifact containing stale success accepted")
	}
}

func TestEvidence_RejectsWrongOrStaleDedicatedArtifact(t *testing.T) {
	dir := t.TempDir()
	writeExecutableEvidence(t, dir, "pr-full", "transfer-100mib", true, true, true)
	writeEvidenceFile(t, dir, "reconnect.json", `{}`)
	if err := ValidateDirectory(dir); err == nil {
		t.Fatal("wrong dedicated artifact accepted")
	}
}

func writeDedicatedExecutableEvidence(t *testing.T, dir, scenarioName, fault string, passed bool) {
	t.Helper()
	metadata := validMetadata(t, dir, scenarioName)
	metadata.Fault = fault
	writeJSONEvidenceFile(t, dir, "metadata.json", metadata)
	errorText := ""
	if !passed {
		if scenarioName == "transfer-100mib" {
			errorText = "transfer scenario: injected hash mismatch"
		} else {
			errorText = "reconnect scenario: injected Dashboard exit"
		}
	}
	definition, err := contract.ScenarioDefinitionByName(scenarioName)
	if err != nil {
		t.Fatalf("scenario definition: %v", err)
	}
	assertions := make([]Assertion, 0, len(definition.Assertions(fault)))
	for _, assertion := range definition.Assertions(fault) {
		assertions = append(assertions, Assertion{Name: assertion.Name, Passed: assertion.Passed})
	}
	results := Results{Profile: "pr-full", Passed: passed, Scenarios: []ScenarioResult{{Name: scenarioName, Passed: passed, Assertions: assertions, Error: errorText}}}
	writeJSONEvidenceFile(t, dir, "results.json", results)
	junit, err := JUnit(results)
	if err != nil {
		t.Fatalf("JUnit: %v", err)
	}
	writeEvidenceFile(t, dir, "junit.xml", string(junit))
	writeEvidenceFile(t, dir, "cleanup.json", fmt.Sprintf(`{"passed":true,"scenario":%q,"finished_at":"2026-01-02T03:04:05Z"}`, scenarioName))
}

func validTransferArtifact(fault string, passed bool) transferArtifact {
	errorText := ""
	if !passed {
		errorText = "transfer scenario: injected hash mismatch"
	}
	evidence := transferEvidence{WarmupUploadBytes: 65536, WarmupDownloadBytes: 65536, WarmupSHA256: "abc", WarmupDuration: time.Nanosecond, WarmupDeadlineRemaining: time.Second, WarmupQuiescent: true, OutsideRootSentinelsUnchanged: true}
	if passed {
		evidence.UploadBytes = 104857600
		evidence.DownloadBytes = 104857600
		evidence.UploadSHA256 = "abc"
		evidence.DownloadSHA256 = "abc"
		evidence.UploadChunks = 2
		evidence.DownloadChunks = 2
		evidence.UploadDuration = time.Second
		evidence.DownloadDuration = time.Second
		evidence.Mode = "0640"
		evidence.CreateDirs = true
		evidence.UploadReplayRejected = true
		evidence.DownloadReplayRejected = true
		evidence.OversizeRejected = true
	}
	return transferArtifact{Scenario: "transfer-100mib", Fault: fault, Passed: passed, CleanupOK: true, Error: errorText, Evidence: evidence}
}

func validReconnectArtifact(t *testing.T, fault string, passed bool) reconnectArtifact {
	t.Helper()
	var artifact reconnectArtifact
	artifact.Scenario = "reconnect"
	artifact.Fault = fault
	artifact.Passed = passed
	artifact.CleanupOK = true
	if !passed {
		artifact.Error = "reconnect scenario: injected Dashboard exit"
	}
	evidence := &artifact.Evidence
	fixtureRoot := t.TempDir()
	// filepath.IsAbs follows the target OS, so fixtures must use native absolute paths.
	dashboardRoot := filepath.Join(fixtureRoot, "dashboard")
	agentRoot := filepath.Join(fixtureRoot, "agent")
	evidence.Fixture.Dashboard.WorkspaceRoot = dashboardRoot
	evidence.Fixture.Dashboard.ConfigPath = filepath.Join(dashboardRoot, "config")
	evidence.Fixture.Dashboard.BinaryPath = filepath.Join(dashboardRoot, "bin")
	evidence.Fixture.AgentRoot = agentRoot
	evidence.Fixture.AgentConfigPath = filepath.Join(agentRoot, "config")
	evidence.Fixture.AgentBinaryPath = filepath.Join(agentRoot, "bin")
	evidence.Runtime.DashboardBefore = runtimeIdentity{Generation: 1, PID: 10, ProcessGroupID: 10}
	evidence.Runtime.AgentBefore = runtimeIdentity{Generation: 1, PID: 20, ProcessGroupID: 20}
	evidence.Identity.ServerID = 1
	evidence.Identity.UUID = "uuid"
	evidence.Lifecycle.DisconnectAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	evidence.Lifecycle.OutsideRootSentinelUnchanged = true
	evidence.AgentCleanup = validCleanupReceipt("agent", expectedProcessCount(passed))
	evidence.DashboardCleanup = validCleanupReceipt("dashboard", expectedProcessCount(passed))
	if passed {
		evidence.Runtime.DashboardAfter = runtimeIdentity{Generation: 2, PID: 11, ProcessGroupID: 11}
		evidence.Runtime.AgentAfter = runtimeIdentity{Generation: 2, PID: 21, ProcessGroupID: 21}
		evidence.Runtime.StateGenerationBeforeAgentRestart = 1
		evidence.Runtime.StateGenerationAfterAgentRestart = 2
		evidence.Identity.DashboardConfigUnchanged = true
		evidence.Identity.AgentConfigUnchanged = true
		evidence.Identity.DashboardFixtureUnchanged = true
		evidence.Identity.ClientsRecreated = true
		evidence.Identity.BootstrapRecreated = true
		evidence.Lifecycle.ReconnectAt = evidence.Lifecycle.DisconnectAt.Add(time.Second)
		evidence.Lifecycle.ReconnectInterval = time.Second
		evidence.Lifecycle.DashboardReceipts = make([]receiptPair, 3)
		evidence.Lifecycle.AgentReceipts = make([]receiptPair, 2)
		evidence.Observation = reconnectObservation{ServerID: 1, UUID: "uuid", OldGeneration: 1, NewGeneration: 2, DisconnectAt: evidence.Lifecycle.DisconnectAt, ReconnectAt: evidence.Lifecycle.ReconnectAt, TaskIDs: []uint64{1, 2, 3, 4, 5}, ResultIDs: []uint64{1, 2, 3, 4, 5}, PostReconnect: true, AgentRestarted: true}
	}
	return artifact
}

func validCleanupReceipt(name string, count int) cleanupReceipt {
	receipt := cleanupReceipt{Passed: true, Processes: make([]cleanupRecord, count)}
	for index := range receipt.Processes {
		receipt.Processes[index] = cleanupRecord{Name: name, PID: index + 1}
	}
	return receipt
}

func expectedProcessCount(passed bool) int {
	if passed {
		return 2
	}
	return 1
}
