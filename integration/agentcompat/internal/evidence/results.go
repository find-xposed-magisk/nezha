package evidence

import (
	"encoding/json"
	"fmt"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type ScenarioResult struct {
	Name       string      `json:"name"`
	Passed     bool        `json:"passed"`
	Assertions []Assertion `json:"assertions,omitempty"`
	Error      string      `json:"error,omitempty"`
}

type Assertion struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details,omitempty"`
}

type Results struct {
	Profile   string           `json:"profile"`
	Passed    bool             `json:"passed"`
	Scenarios []ScenarioResult `json:"scenarios"`
}

func (results Results) Validate() error {
	if results.Profile == "" || len(results.Scenarios) == 0 {
		return fmt.Errorf("results fields are incomplete")
	}
	if _, err := contract.ProfileByName(results.Profile); err != nil {
		return fmt.Errorf("results profile is invalid: %w", err)
	}
	allPassed := true
	seenScenarios := make(map[string]struct{}, len(results.Scenarios))
	for _, scenario := range results.Scenarios {
		if _, err := contract.NewScenario(scenario.Name); err != nil {
			return fmt.Errorf("scenario name is invalid: %w", err)
		}
		if _, exists := seenScenarios[scenario.Name]; exists {
			return fmt.Errorf("scenario name is duplicated")
		}
		seenScenarios[scenario.Name] = struct{}{}
		if len(scenario.Assertions) == 0 {
			return fmt.Errorf("scenario must contain assertions")
		}
		assertionsPassed := true
		seenAssertions := make(map[string]struct{}, len(scenario.Assertions))
		for _, assertion := range scenario.Assertions {
			if assertion.Name == "" {
				return fmt.Errorf("assertion name is required")
			}
			if _, exists := seenAssertions[assertion.Name]; exists {
				return fmt.Errorf("assertion name is duplicated")
			}
			seenAssertions[assertion.Name] = struct{}{}
			assertionsPassed = assertionsPassed && assertion.Passed
		}
		if scenario.Passed != assertionsPassed {
			return fmt.Errorf("scenario pass state is inconsistent with assertions")
		}
		if !scenario.Passed {
			if scenario.Error == "" {
				return fmt.Errorf("failed scenario error is required")
			}
			allPassed = false
		} else if scenario.Error != "" {
			return fmt.Errorf("passed scenario cannot contain an error")
		}
	}
	if results.Passed != allPassed {
		return fmt.Errorf("results pass state is inconsistent")
	}
	return nil
}

func MarshalResults(results Results) ([]byte, error) {
	if err := results.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(results)
}

func (results Results) MarshalJSON() ([]byte, error) {
	type resultsWire Results
	redacted := resultsWire{Profile: results.Profile, Passed: results.Passed, Scenarios: make([]ScenarioResult, 0, len(results.Scenarios))}
	for _, scenario := range results.Scenarios {
		assertions := make([]Assertion, 0, len(scenario.Assertions))
		for _, assertion := range scenario.Assertions {
			assertions = append(assertions, Assertion{Name: assertion.Name, Passed: assertion.Passed, Details: Redact(assertion.Details)})
		}
		redacted.Scenarios = append(redacted.Scenarios, ScenarioResult{Name: scenario.Name, Passed: scenario.Passed, Assertions: assertions, Error: Redact(scenario.Error)})
	}
	return json.Marshal(redacted)
}

func scenarioResultNames(scenarios []ScenarioResult) []string {
	names := make([]string, 0, len(scenarios))
	for _, scenario := range scenarios {
		names = append(names, scenario.Name)
	}
	return names
}
