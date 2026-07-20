package contract

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type NezhaSourcePath struct{ value string }
type AgentSourcePath struct{ value string }
type ResultsPath struct{ value string }

type Paths struct {
	nezhaSource NezhaSourcePath
	agentSource AgentSourcePath
	resultsDir  ResultsPath
}

func NewPaths(nezhaSource, agentSource, resultsDir string) (Paths, error) {
	nezha, err := cleanAbsolutePath(nezhaSource)
	if err != nil {
		return Paths{}, errors.New("invalid --nezha-source path")
	}
	agent, err := cleanAbsolutePath(agentSource)
	if err != nil {
		return Paths{}, errors.New("invalid --agent-source path")
	}
	results, err := cleanAbsolutePath(resultsDir)
	if err != nil {
		return Paths{}, errors.New("invalid --results-dir path")
	}
	return Paths{nezhaSource: NezhaSourcePath{value: nezha}, agentSource: AgentSourcePath{value: agent}, resultsDir: ResultsPath{value: results}}, nil
}

func cleanAbsolutePath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || !filepath.IsAbs(raw) {
		return "", errors.New("path must be absolute")
	}
	return filepath.Clean(raw), nil
}

func (p Paths) NezhaSource() NezhaSourcePath { return p.nezhaSource }
func (p Paths) AgentSource() AgentSourcePath { return p.agentSource }
func (p Paths) ResultsDir() ResultsPath      { return p.resultsDir }
func (p NezhaSourcePath) String() string     { return p.value }
func (p AgentSourcePath) String() string     { return p.value }
func (p ResultsPath) String() string         { return p.value }

type Scenario struct{ value string }

func NewScenario(raw string) (Scenario, error) {
	if !namePattern.MatchString(raw) {
		return Scenario{}, errors.New("invalid scenario name")
	}
	return Scenario{value: raw}, nil
}

func (s Scenario) String() string { return s.value }

type Fault struct{ value string }

func NewFault(raw string) (Fault, error) {
	if !namePattern.MatchString(raw) {
		return Fault{}, errors.New("invalid fault name")
	}
	return Fault{value: raw}, nil
}

func (f Fault) String() string { return f.value }
func (f Fault) IsZero() bool   { return f.value == "" }
