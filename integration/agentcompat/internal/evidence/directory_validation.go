package evidence

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type cleanupEvidence struct {
	Passed     bool   `json:"passed"`
	Scenario   string `json:"scenario"`
	FinishedAt string `json:"finished_at"`
}

type currentEvidenceProfile struct {
	RequiredFiles []string
	DedicatedFile string
	Executable    bool
}

func currentProfile(metadata Metadata) (currentEvidenceProfile, error) {
	if len(metadata.Scenarios) == 0 {
		return currentEvidenceProfile{}, errors.New("metadata scenarios are required")
	}
	if len(metadata.Scenarios) == 1 && metadata.Scenarios[0] == contract.ScenarioMetadata {
		if metadata.Fault != "" {
			return currentEvidenceProfile{}, errors.New("metadata scenario does not support fault injection")
		}
		return currentEvidenceProfile{RequiredFiles: []string{"metadata.json"}}, nil
	}
	if len(metadata.Scenarios) != 1 {
		return currentEvidenceProfile{}, errors.New("current evidence profile requires one supported scenario")
	}
	scenarioValue, err := contract.NewScenario(metadata.Scenarios[0])
	if err != nil {
		return currentEvidenceProfile{}, fmt.Errorf("construct metadata scenario: %w", err)
	}
	if !contract.IsSupportedScenario(metadata.Scenarios[0]) || metadata.Scenarios[0] == contract.ScenarioMetadata {
		return currentEvidenceProfile{}, errors.New("current evidence profile requires one supported scenario")
	}
	faultValue := contract.Fault{}
	if metadata.Fault != "" {
		faultValue, err = contract.NewFault(metadata.Fault)
		if err != nil {
			return currentEvidenceProfile{}, fmt.Errorf("construct metadata fault: %w", err)
		}
	}
	if err := contract.ValidateScenarioFault(scenarioValue, faultValue); err != nil {
		return currentEvidenceProfile{}, err
	}
	definition, err := contract.ScenarioDefinitionByName(metadata.Scenarios[0])
	if err != nil {
		return currentEvidenceProfile{}, err
	}
	profile := currentEvidenceProfile{RequiredFiles: []string{"metadata.json", "results.json", "junit.xml", "cleanup.json"}, DedicatedFile: definition.DedicatedArtifactName(), Executable: true}
	if profile.DedicatedFile != "" {
		profile.RequiredFiles = append(profile.RequiredFiles, profile.DedicatedFile)
	}
	return profile, nil
}

func ValidateDirectory(resultsDir string) error {
	files, err := scanDirectory(resultsDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("evidence directory contains no files")
	}
	metadata, err := readJSONFile[Metadata](resultsDir, "metadata.json")
	if err != nil {
		return err
	}
	if err := metadata.Validate(); err != nil {
		return fmt.Errorf("validate metadata evidence: %w", err)
	}
	profile, err := currentProfile(metadata)
	if err != nil {
		return err
	}
	for _, required := range profile.RequiredFiles {
		if _, exists := files[required]; !exists {
			return fmt.Errorf("required evidence file is missing: %s", required)
		}
	}
	if err := rejectStaleDedicatedFiles(files, profile.DedicatedFile); err != nil {
		return err
	}
	if !profile.Executable {
		return nil
	}
	results, err := readJSONFile[Results](resultsDir, "results.json")
	if err != nil {
		return err
	}
	if err := results.Validate(); err != nil {
		return fmt.Errorf("validate results evidence: %w", err)
	}
	if results.Profile != metadata.Profile.Name || !slices.Equal(metadata.Scenarios, scenarioResultNames(results.Scenarios)) {
		return errors.New("metadata and results do not agree")
	}
	if err := validateJUnit(resultsDir, results); err != nil {
		return err
	}
	cleanup, err := readJSONFile[cleanupEvidence](resultsDir, "cleanup.json")
	if err != nil {
		return err
	}
	if !cleanup.Passed || cleanup.Scenario != results.Scenarios[0].Name {
		return errors.New("cleanup evidence is missing or failed")
	}
	if _, err := time.Parse(time.RFC3339, cleanup.FinishedAt); err != nil {
		return errors.New("cleanup finish time is invalid")
	}
	if profile.DedicatedFile != "" {
		definition, err := contract.ScenarioDefinitionByName(results.Scenarios[0].Name)
		if err != nil {
			return err
		}
		if err := validateScenarioAssertions(results.Scenarios[0], definition.Assertions(metadata.Fault)); err != nil {
			return err
		}
		return validateDedicatedArtifact(resultsDir, metadata, results.Scenarios[0])
	}
	return nil
}

func validateScenarioAssertions(result ScenarioResult, expected []contract.AssertionDefinition) error {
	if len(result.Assertions) != len(expected) {
		return errors.New("scenario assertions do not match dedicated evidence contract")
	}
	for index, assertion := range result.Assertions {
		if assertion.Name != expected[index].Name || assertion.Passed != expected[index].Passed {
			return errors.New("scenario assertions do not match dedicated evidence contract")
		}
	}
	return nil
}

func rejectStaleDedicatedFiles(files map[string]os.FileInfo, expected string) error {
	for _, name := range []string{"transfer.json", "reconnect.json"} {
		if _, exists := files[name]; exists && name != expected {
			return fmt.Errorf("stale or wrong dedicated evidence file: %s", name)
		}
	}
	return nil
}

func validateJUnit(resultsDir string, results Results) error {
	data, err := os.ReadFile(filepath.Join(resultsDir, "junit.xml"))
	if err != nil {
		return fmt.Errorf("read JUnit evidence: %w", err)
	}
	var suite junitSuite
	if err := xml.Unmarshal(data, &suite); err != nil {
		return fmt.Errorf("parse JUnit evidence: %w", err)
	}
	if suite.Name != results.Profile || suite.Tests != len(results.Scenarios) || suite.Failures != countFailedScenarios(results.Scenarios) || len(suite.Cases) != len(results.Scenarios) {
		return errors.New("results and JUnit evidence do not agree")
	}
	for index, scenario := range results.Scenarios {
		caseResult := suite.Cases[index]
		if caseResult.Name != scenario.Name || (caseResult.Failure != nil) != !scenario.Passed {
			return errors.New("results and JUnit scenario states do not agree")
		}
	}
	return nil
}
