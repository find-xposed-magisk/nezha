//go:build linux

package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

var ErrConfigIdentityChanged = errors.New("scenario: config identity changed")

type AgentConfigSnapshot struct {
	Debug        bool
	ReportDelay  uint32
	ClientSecret string
	UUID         string
	Server       string
}

type AgentConfig struct {
	Debug        bool   `json:"debug" yaml:"debug"`
	Server       string `json:"server" yaml:"server"`
	ClientSecret string `json:"client_secret" yaml:"client_secret"`
	UUID         string `json:"uuid" yaml:"uuid"`
	ReportDelay  uint32 `json:"report_delay" yaml:"report_delay"`
	TLS          bool   `json:"tls" yaml:"tls"`
	InsecureTLS  bool   `json:"insecure_tls" yaml:"insecure_tls"`
}

type ConfigDiffResult struct {
	DebugChanged       bool
	ReportDelayChanged bool
}

func ConfigDiff(original, updated AgentConfigSnapshot) (ConfigDiffResult, error) {
	if original.ClientSecret != updated.ClientSecret || original.UUID != updated.UUID || original.Server != updated.Server {
		return ConfigDiffResult{}, ErrConfigIdentityChanged
	}
	return ConfigDiffResult{DebugChanged: original.Debug != updated.Debug, ReportDelayChanged: original.ReportDelay != updated.ReportDelay}, nil
}

type Assertion struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details,omitempty"`
}

type Result struct {
	Name       string      `json:"name"`
	Passed     bool        `json:"passed"`
	Assertions []Assertion `json:"assertions"`
	CleanupOK  bool        `json:"cleanup_ok"`
	Error      string      `json:"error,omitempty"`
}

type AssertionSet struct {
	assertions []Assertion
}

func NewAssertionSet() *AssertionSet { return &AssertionSet{} }

func (set *AssertionSet) Record(name string, passed bool, details string) {
	set.assertions = append(set.assertions, Assertion{Name: name, Passed: passed, Details: evidence.Redact(details)})
}

func (set *AssertionSet) Results() []Assertion {
	return append([]Assertion(nil), set.assertions...)
}

func (set *AssertionSet) Run(run func(*AssertionSet) error) error { return run(set) }

func configSnapshot(config AgentConfig) AgentConfigSnapshot {
	return AgentConfigSnapshot{Debug: config.Debug, ReportDelay: config.ReportDelay, ClientSecret: config.ClientSecret, UUID: config.UUID, Server: config.Server}
}

func changedOnlyDebugAndReportDelay(original, updated AgentConfig) error {
	before := original
	after := updated
	before.Debug = after.Debug
	before.ReportDelay = after.ReportDelay
	if !reflect.DeepEqual(before, after) {
		return errors.New("config update changed fields other than debug and report_delay")
	}
	return nil
}

func decodeAgentConfig(raw string) (AgentConfig, error) {
	var config AgentConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return AgentConfig{}, err
	}
	return config, nil
}

type Context struct {
	Context context.Context
}
