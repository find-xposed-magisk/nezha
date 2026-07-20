package evidence

import "encoding/xml"

type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name    string        `xml:"name,attr"`
	Failure *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
}

func JUnit(results Results) ([]byte, error) {
	if err := results.Validate(); err != nil {
		return nil, err
	}
	suite := junitSuite{Name: results.Profile, Tests: len(results.Scenarios)}
	for _, scenario := range results.Scenarios {
		caseResult := junitCase{Name: scenario.Name}
		if !scenario.Passed {
			suite.Failures++
			caseResult.Failure = &junitFailure{Message: Redact(scenario.Error)}
		}
		suite.Cases = append(suite.Cases, caseResult)
	}
	return xml.Marshal(suite)
}

func countFailedScenarios(scenarios []ScenarioResult) int {
	failed := 0
	for _, scenario := range scenarios {
		if !scenario.Passed {
			failed++
		}
	}
	return failed
}
