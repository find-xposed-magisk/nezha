package evidence

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
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
	if strings.TrimSpace(resultsDir) == "" {
		return errors.New("evidence directory is required")
	}
	info, err := os.Lstat(resultsDir)
	if err != nil {
		return fmt.Errorf("stat evidence directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("evidence path must be a directory")
	}
	root, err := os.OpenRoot(resultsDir)
	if err != nil {
		return fmt.Errorf("open evidence directory: %w", err)
	}
	defer root.Close()
	files, err := scanDirectory(root)
	if err != nil {
		return err
	}
	return validateSnapshot(files)
}

func validateSnapshot(files evidenceSnapshot) error {
	if len(files) == 0 {
		return errors.New("evidence directory contains no files")
	}
	metadata, err := readJSONFile[Metadata](files, "metadata.json")
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
	results, err := readJSONFile[Results](files, "results.json")
	if err != nil {
		return err
	}
	if err := results.Validate(); err != nil {
		return fmt.Errorf("validate results evidence: %w", err)
	}
	if results.Profile != metadata.Profile.Name || !slices.Equal(metadata.Scenarios, scenarioResultNames(results.Scenarios)) {
		return errors.New("metadata and results do not agree")
	}
	if err := validateJUnit(files, results); err != nil {
		return err
	}
	cleanup, err := readJSONFile[cleanupEvidence](files, "cleanup.json")
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
		return validateDedicatedArtifact(files, metadata, results.Scenarios[0])
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

func rejectStaleDedicatedFiles(files evidenceSnapshot, expected string) error {
	for _, name := range []string{"transfer.json", "reconnect.json"} {
		if _, exists := files[name]; exists && name != expected {
			return fmt.Errorf("stale or wrong dedicated evidence file: %s", name)
		}
	}
	return nil
}

func validateJUnit(files evidenceSnapshot, results Results) error {
	file, exists := files["junit.xml"]
	if !exists {
		return errors.New("read JUnit evidence: evidence snapshot is missing")
	}
	var suite junitSuite
	if err := xml.Unmarshal(file.data, &suite); err != nil {
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
