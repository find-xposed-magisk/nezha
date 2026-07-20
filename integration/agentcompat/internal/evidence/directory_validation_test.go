package evidence

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestEvidence_CurrentProfilePropagatesMalformedScenarioAndFault(t *testing.T) {
	tests := []struct {
		name        string
		metadata    Metadata
		wantContext string
		wantCause   string
	}{
		{name: "scenario", metadata: Metadata{Scenarios: []string{"invalid scenario"}}, wantContext: "construct metadata scenario", wantCause: "invalid scenario name"},
		{name: "fault", metadata: Metadata{Scenarios: []string{contract.ScenarioTransfer100MiB}, Fault: "invalid fault"}, wantContext: "construct metadata fault", wantCause: "invalid fault name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := currentProfile(test.metadata)
			if err == nil || !strings.Contains(err.Error(), test.wantContext) || !strings.Contains(err.Error(), test.wantCause) {
				t.Fatalf("currentProfile error=%v, want context %q and cause %q", err, test.wantContext, test.wantCause)
			}
		})
	}
}

func TestEvidence_NoCredentialsInDirectory(t *testing.T) {
	if len(flag.Args()) == 0 {
		t.Skip("requires a results directory argument: go test ./integration/agentcompat/internal/evidence -run TestEvidence_NoCredentialsInDirectory -args RESULTS")
	}
	if err := ValidateDirectory(flag.Args()[0]); err != nil {
		t.Fatalf("validate evidence directory: %v", err)
	}
}

func TestEvidence_NoCredentialsInDirectoryFixture(t *testing.T) {
	resultsDir := t.TempDir()
	metadata := validMetadata(t, resultsDir, contract.ScenarioRegistrationConfigExec)
	writeJSONEvidenceFile(t, resultsDir, "metadata.json", metadata)
	results := Results{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: contract.ScenarioRegistrationConfigExec, Passed: true, Assertions: []Assertion{{Name: "safe fixture", Passed: true}}}}}
	writeJSONEvidenceFile(t, resultsDir, "results.json", results)
	junit, err := JUnit(results)
	if err != nil {
		t.Fatalf("marshal safe JUnit: %v", err)
	}
	writeEvidenceFile(t, resultsDir, "junit.xml", string(junit))
	writeEvidenceFile(t, resultsDir, "cleanup.json", `{"passed":true,"scenario":"registration-config-exec","finished_at":"2026-01-02T03:04:05Z"}`)
	if err := ValidateDirectory(resultsDir); err != nil {
		t.Fatalf("validate evidence directory: %v", err)
	}
}

func TestEvidence_ValidateDirectoryRejectsCrossFileMismatches(t *testing.T) {
	tests := map[string]func(*testing.T, string){
		"profile mismatch": func(t *testing.T, dir string) {
			writeExecutableEvidence(t, dir, "soak", contract.ScenarioRegistrationConfigExec, true, true, true)
		},
		"scenario mismatch": func(t *testing.T, dir string) {
			writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, true, false, true)
		},
		"junit mismatch": func(t *testing.T, dir string) {
			writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, false, true, true)
		},
		"missing cleanup": func(t *testing.T, dir string) {
			writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, true, true, false)
		},
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setup(t, dir)
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("inconsistent evidence accepted")
			}
		})
	}
}

func TestEvidence_ValidateDirectoryRejectsFailedCleanup(t *testing.T) {
	dir := t.TempDir()
	writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, true, true, true)
	writeEvidenceFile(t, dir, "cleanup.json", `{"passed":false,"scenario":"registration-config-exec","finished_at":"2026-01-02T03:04:05Z"}`)
	if err := ValidateDirectory(dir); err == nil {
		t.Fatal("failed cleanup accepted")
	}
}

func TestEvidence_MCPFilesystemExecutableProfileValidates(t *testing.T) {
	dir := t.TempDir()
	writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioMCPFilesystem, true, true, true)
	if err := ValidateDirectory(dir); err != nil {
		t.Fatalf("validate mcp-filesystem evidence: %v", err)
	}
}

func TestEvidence_NoCredentialsInDirectoryRejectsMissingPathAndFiles(t *testing.T) {
	if err := ValidateDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing evidence directory accepted")
	}
	for name, dir := range map[string]string{"empty": t.TempDir(), "incomplete": t.TempDir()} {
		if name == "incomplete" {
			writeEvidenceFile(t, dir, "metadata.json", `{ "ok": true }`)
		}
		if err := ValidateDirectory(dir); err == nil {
			t.Fatalf("%s evidence directory accepted", name)
		}
	}
}

func TestEvidence_NoCredentialsInDirectoryRejectsCredentialsAndMalformedDocuments(t *testing.T) {
	tests := []struct{ name, results, junit string }{
		{"credentials", `{ "token": "secret-value" }`, `<testsuite></testsuite>`},
		{"malformed json", `{ malformed`, `<testsuite></testsuite>`},
		{"malformed xml", `{ "ok": true }`, `<testsuite>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeEvidenceFile(t, dir, "metadata.json", `{ "ok": true }`)
			writeEvidenceFile(t, dir, "results.json", test.results)
			writeEvidenceFile(t, dir, "junit.xml", test.junit)
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func writeEvidenceFile(t *testing.T, resultsDir, name, content string) {
	t.Helper()
	if err := os.Chmod(resultsDir, 0o700); err != nil {
		t.Fatalf("secure evidence root: %v", err)
	}
	path := filepath.Join(resultsDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create evidence parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write evidence file: %v", err)
	}
}

func writeJSONEvidenceFile(t *testing.T, resultsDir, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	writeEvidenceFile(t, resultsDir, name, string(data))
}

func validMetadata(t *testing.T, resultsDir, scenarioName string) Metadata {
	t.Helper()
	profile, err := contract.ProfileByName("pr-full")
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	paths, err := contract.NewPaths("/src/nezha", "/src/agent", resultsDir)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	scenarioValue, err := contract.NewScenario(scenarioName)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	metadata, err := NewMetadata(MetadataInput{Profile: profile, Seed: contract.DefaultSeed, Paths: paths, ResourceBudget: contract.DefaultResourceBudget(), Scenarios: []contract.Scenario{scenarioValue}, StartedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EvidenceFiles: EvidenceFiles()})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	return metadata
}

func writeExecutableEvidence(t *testing.T, dir, profileName, scenarioName string, matchingJUnit, matchingScenario, writeCleanup bool) {
	t.Helper()
	metadata := validMetadata(t, dir, scenarioName)
	metadata.Profile.Name = profileName
	writeJSONEvidenceFile(t, dir, "metadata.json", metadata)
	resultsScenario := scenarioName
	if !matchingScenario {
		resultsScenario = contract.ScenarioMetadata
	}
	results := Results{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: resultsScenario, Passed: true, Assertions: []Assertion{{Name: "fixture", Passed: true}}}}}
	writeJSONEvidenceFile(t, dir, "results.json", results)
	junit, err := JUnit(results)
	if err != nil {
		t.Fatalf("JUnit: %v", err)
	}
	if !matchingJUnit {
		junit = []byte(`<testsuite name="pr-full" tests="1" failures="1"><testcase name="other"><failure message="bad"></failure></testcase></testsuite>`)
	}
	writeEvidenceFile(t, dir, "junit.xml", string(junit))
	if writeCleanup {
		writeEvidenceFile(t, dir, "cleanup.json", fmt.Sprintf(`{"passed":true,"scenario":%q,"finished_at":"2026-01-02T03:04:05Z"}`, scenarioName))
	}
}
