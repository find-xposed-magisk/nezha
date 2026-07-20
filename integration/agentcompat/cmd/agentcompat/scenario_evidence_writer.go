package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/scenario"
)

type transferArtifact struct {
	Scenario  string                    `json:"scenario"`
	Fault     string                    `json:"fault,omitempty"`
	Passed    bool                      `json:"passed"`
	CleanupOK bool                      `json:"cleanup_ok"`
	Error     string                    `json:"error,omitempty"`
	Evidence  scenario.TransferEvidence `json:"evidence"`
}

type reconnectArtifact struct {
	Scenario  string                     `json:"scenario"`
	Fault     string                     `json:"fault,omitempty"`
	Passed    bool                       `json:"passed"`
	CleanupOK bool                       `json:"cleanup_ok"`
	Error     string                     `json:"error,omitempty"`
	Evidence  scenario.ReconnectEvidence `json:"evidence"`
}

func writeScenarioEvidence(config cliConfig, output scenarioExecutionOutput, now time.Time) error {
	if err := output.Validate(); err != nil {
		return fmt.Errorf("validate scenario execution output: %w", err)
	}
	result := output.Result
	if !result.Passed && config.Fault.String() != "" && allAssertionsPassed(result.Assertions) {
		result.Assertions = append(result.Assertions, scenario.Assertion{Name: contract.AssertionInjectedFault, Passed: false, Details: result.Error})
	}
	if output.Transfer != nil {
		artifact := transferArtifact{Scenario: result.Name, Fault: config.Fault.String(), Passed: result.Passed, CleanupOK: result.CleanupOK, Error: evidence.Redact(result.Error), Evidence: *output.Transfer}
		if err := writeJSONArtifact(config.Paths.ResultsDir().String(), "transfer.json", artifact); err != nil {
			return err
		}
	}
	if output.Reconnect != nil {
		artifact := reconnectArtifact{Scenario: result.Name, Fault: config.Fault.String(), Passed: result.Passed, CleanupOK: result.CleanupOK, Error: evidence.Redact(result.Error), Evidence: *output.Reconnect}
		if err := writeJSONArtifact(config.Paths.ResultsDir().String(), "reconnect.json", artifact); err != nil {
			return err
		}
	}
	assertions := make([]evidence.Assertion, 0, len(result.Assertions))
	for _, assertion := range result.Assertions {
		assertions = append(assertions, evidence.Assertion{Name: assertion.Name, Passed: assertion.Passed, Details: assertion.Details})
	}
	results := evidence.Results{Profile: string(config.Profile.Name()), Passed: result.Passed, Scenarios: []evidence.ScenarioResult{{Name: result.Name, Passed: result.Passed, Assertions: assertions, Error: result.Error}}}
	data, err := evidence.MarshalResults(results)
	if err != nil {
		return fmt.Errorf("marshal scenario results: %w", err)
	}
	if err := writePrivateFile(filepath.Join(config.Paths.ResultsDir().String(), "results.json"), data); err != nil {
		return fmt.Errorf("write scenario results: %w", err)
	}
	junit, err := evidence.JUnit(results)
	if err != nil {
		return fmt.Errorf("marshal scenario junit: %w", err)
	}
	if err := writePrivateFile(filepath.Join(config.Paths.ResultsDir().String(), "junit.xml"), junit); err != nil {
		return fmt.Errorf("write scenario junit: %w", err)
	}
	cleanup := struct {
		Passed     bool   `json:"passed"`
		Scenario   string `json:"scenario"`
		FinishedAt string `json:"finished_at"`
	}{Passed: result.CleanupOK, Scenario: result.Name, FinishedAt: now.UTC().Format(time.RFC3339)}
	return writeJSONArtifact(config.Paths.ResultsDir().String(), "cleanup.json", cleanup)
}

func writeJSONArtifact(resultsDir, name string, artifact any) error {
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	if redacted := evidence.Redact(string(data)); redacted != string(data) {
		return fmt.Errorf("credential detected while marshaling %s", name)
	}
	if err := writePrivateFile(filepath.Join(resultsDir, name), data); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func writePrivateFile(path string, data []byte) error {
	return writePrivateArtifact(path, data)
}

func allAssertionsPassed(assertions []scenario.Assertion) bool {
	for _, assertion := range assertions {
		if !assertion.Passed {
			return false
		}
	}
	return true
}
