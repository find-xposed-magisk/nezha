package evidence

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type MetadataInput struct {
	Profile        contract.Profile
	Seed           contract.Seed
	Paths          contract.Paths
	ResourceBudget contract.ResourceBudget
	Scenarios      []contract.Scenario
	Fault          contract.Fault
	StartedAt      time.Time
	EvidenceFiles  []string
}

type Metadata struct {
	AgentSource        string                 `json:"agent_source"`
	EvidenceFiles      []string               `json:"evidence_files"`
	LoadClassification string                 `json:"load_classification"`
	NezhaSource        string                 `json:"nezha_source"`
	Profile            ProfileMetadata        `json:"profile"`
	ResourceBudget     ResourceBudgetMetadata `json:"resource_budget"`
	ResultsDir         string                 `json:"results_dir"`
	Scenarios          []string               `json:"scenarios"`
	Seed               string                 `json:"seed"`
	Fault              string                 `json:"fault,omitempty"`
	StartedAt          string                 `json:"started_at"`
}

func NewMetadata(input MetadataInput) (Metadata, error) {
	metadata := Metadata{
		AgentSource:        Redact(input.Paths.AgentSource().String()),
		EvidenceFiles:      append([]string(nil), input.EvidenceFiles...),
		LoadClassification: "regression loads, not capacity claims",
		NezhaSource:        Redact(input.Paths.NezhaSource().String()),
		Profile:            profileMetadata(input.Profile),
		ResourceBudget:     resourceBudgetMetadata(input.ResourceBudget),
		ResultsDir:         Redact(input.Paths.ResultsDir().String()),
		Scenarios:          scenarioNames(input.Scenarios),
		Seed:               fmt.Sprintf("0x%x", uint64(input.Seed)),
		Fault:              input.Fault.String(),
		StartedAt:          input.StartedAt.UTC().Format(time.RFC3339),
	}
	if err := metadata.Validate(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func EvidenceFiles() []string {
	files := []string{"metadata.json", "results.json", "junit.xml", "dashboard.log", "agents/*.log"}
	for _, definition := range contract.ScenarioDefinitions() {
		if name := definition.DedicatedArtifactName(); name != "" {
			files = append(files, name)
		}
	}
	return append(files, "stress.json", "cleanup.json", "step-summary.md")
}

func FixedEvidenceFiles() []string {
	files := make([]string, 0, len(EvidenceFiles()))
	for _, name := range EvidenceFiles() {
		if !slices.Contains([]rune(name), '*') {
			files = append(files, name)
		}
	}
	return files
}

func scenarioNames(scenarios []contract.Scenario) []string {
	names := make([]string, 0, len(scenarios))
	for _, scenario := range scenarios {
		names = append(names, scenario.String())
	}
	return names
}

func (metadata Metadata) Validate() error {
	if metadata.AgentSource == "" || len(metadata.EvidenceFiles) == 0 || metadata.LoadClassification == "" || metadata.NezhaSource == "" || metadata.ResultsDir == "" || metadata.Seed == "" || metadata.Seed == "0x0" || metadata.StartedAt == "" {
		return fmt.Errorf("metadata fields are incomplete")
	}
	if err := metadata.Profile.Validate(); err != nil {
		return err
	}
	if _, err := contract.ProfileByName(metadata.Profile.Name); err != nil {
		return fmt.Errorf("metadata profile is invalid: %w", err)
	}
	if err := metadata.ResourceBudget.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(metadata.AgentSource) || !filepath.IsAbs(metadata.NezhaSource) || !filepath.IsAbs(metadata.ResultsDir) {
		return fmt.Errorf("metadata paths must be absolute")
	}
	if _, err := time.Parse(time.RFC3339, metadata.StartedAt); err != nil {
		return fmt.Errorf("metadata start time is invalid: %w", err)
	}
	if !slices.Equal(metadata.EvidenceFiles, EvidenceFiles()) {
		return fmt.Errorf("metadata evidence files are invalid")
	}
	seen := make(map[string]struct{}, len(metadata.Scenarios))
	for _, scenarioName := range metadata.Scenarios {
		_, err := contract.NewScenario(scenarioName)
		if err != nil {
			return fmt.Errorf("metadata scenario is invalid: %w", err)
		}
		if _, exists := seen[scenarioName]; exists {
			return fmt.Errorf("metadata scenario is duplicated")
		}
		seen[scenarioName] = struct{}{}
	}
	if metadata.Fault != "" {
		if _, err := contract.NewFault(metadata.Fault); err != nil {
			return fmt.Errorf("metadata fault is invalid: %w", err)
		}
	}
	return nil
}

func (metadata Metadata) MarshalJSON() ([]byte, error) {
	type metadataWire Metadata
	redacted := metadataWire(metadata)
	redacted.AgentSource = Redact(redacted.AgentSource)
	redacted.NezhaSource = Redact(redacted.NezhaSource)
	redacted.ResultsDir = Redact(redacted.ResultsDir)
	return json.Marshal(redacted)
}
