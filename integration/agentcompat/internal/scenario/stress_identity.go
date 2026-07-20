//go:build linux

package scenario

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrStressIdentity = errors.New("stress identity is invalid")

type StressAgentOrdinal struct{ value int }

func NewStressAgentOrdinal(value int) (StressAgentOrdinal, error) {
	if value < 1 {
		return StressAgentOrdinal{}, fmt.Errorf("agent ordinal %d: %w", value, ErrStressIdentity)
	}
	return StressAgentOrdinal{value: value}, nil
}

func (ordinal StressAgentOrdinal) Int() int { return ordinal.value }

func (ordinal StressAgentOrdinal) MarshalJSON() ([]byte, error) { return json.Marshal(ordinal.value) }

func (ordinal *StressAgentOrdinal) UnmarshalJSON(data []byte) error {
	var value int
	if err := json.Unmarshal(data, &value); err != nil || value < 1 {
		return ErrStressIdentity
	}
	ordinal.value = value
	return nil
}

type StressOperationID struct{ value string }

func NewStressOperationID(value string) (StressOperationID, error) {
	if value == "" {
		return StressOperationID{}, ErrStressIdentity
	}
	return StressOperationID{value: value}, nil
}

func (identity StressOperationID) String() string { return identity.value }

func (identity StressOperationID) MarshalJSON() ([]byte, error) { return json.Marshal(identity.value) }

func (identity *StressOperationID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil || value == "" {
		return ErrStressIdentity
	}
	identity.value = value
	return nil
}

type StressSessionID struct{ value string }

func NewStressSessionID(value string) (StressSessionID, error) {
	if value == "" {
		return StressSessionID{}, ErrStressIdentity
	}
	return StressSessionID{value: value}, nil
}

func (identity StressSessionID) String() string { return identity.value }

func (identity StressSessionID) MarshalJSON() ([]byte, error) { return json.Marshal(identity.value) }

func (identity *StressSessionID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil || value == "" {
		return ErrStressIdentity
	}
	identity.value = value
	return nil
}

type StressPATID struct{ value string }

func NewStressPATID(value string) (StressPATID, error) {
	if value == "" {
		return StressPATID{}, ErrStressIdentity
	}
	return StressPATID{value: value}, nil
}

func (identity StressPATID) String() string { return identity.value }

func (identity StressPATID) MarshalJSON() ([]byte, error) { return json.Marshal(identity.value) }

func (identity *StressPATID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil || value == "" {
		return ErrStressIdentity
	}
	identity.value = value
	return nil
}

type StressOperationKind string

const (
	StressOperationExec       StressOperationKind = "exec"
	StressOperationFilesystem StressOperationKind = "filesystem"
)

type StressSessionKind string

const (
	StressSessionTerminal StressSessionKind = "terminal"
	StressSessionNAT      StressSessionKind = "nat"
	StressSessionFM       StressSessionKind = "file-manager"
)

type StressProcessKind string

const (
	StressProcessDashboard StressProcessKind = "dashboard"
	StressProcessAgent     StressProcessKind = "agent"
)

type StressProcessIdentity struct {
	Kind  StressProcessKind  `json:"kind"`
	Agent StressAgentOrdinal `json:"agent,omitempty"`
	PID   int                `json:"pid"`
}

func (identity StressProcessIdentity) MarshalJSON() ([]byte, error) {
	type processWire struct {
		Kind  StressProcessKind   `json:"kind"`
		Agent *StressAgentOrdinal `json:"agent,omitempty"`
		PID   int                 `json:"pid"`
	}
	var agent *StressAgentOrdinal
	if identity.Kind == StressProcessAgent {
		if identity.Agent.Int() < 1 {
			return nil, ErrStressIdentity
		}
		agent = &identity.Agent
	}
	return json.Marshal(processWire{Kind: identity.Kind, Agent: agent, PID: identity.PID})
}

func (identity *StressProcessIdentity) UnmarshalJSON(data []byte) error {
	type processWire struct {
		Kind  StressProcessKind   `json:"kind"`
		Agent *StressAgentOrdinal `json:"agent"`
		PID   int                 `json:"pid"`
	}
	var wire processWire
	if err := json.Unmarshal(data, &wire); err != nil || wire.PID < 1 {
		return ErrStressIdentity
	}
	switch wire.Kind {
	case StressProcessDashboard:
		*identity = StressProcessIdentity{Kind: wire.Kind, PID: wire.PID}
	case StressProcessAgent:
		if wire.Agent == nil || wire.Agent.Int() < 1 {
			return ErrStressIdentity
		}
		*identity = StressProcessIdentity{Kind: wire.Kind, Agent: *wire.Agent, PID: wire.PID}
	default:
		return ErrStressIdentity
	}
	return nil
}

func NewStressDashboardProcess(pid int) (StressProcessIdentity, error) {
	if pid < 1 {
		return StressProcessIdentity{}, fmt.Errorf("dashboard PID %d: %w", pid, ErrStressIdentity)
	}
	return StressProcessIdentity{Kind: StressProcessDashboard, PID: pid}, nil
}

func NewStressAgentProcess(agent StressAgentOrdinal, pid int) (StressProcessIdentity, error) {
	if agent.Int() < 1 || pid < 1 {
		return StressProcessIdentity{}, fmt.Errorf("agent process ordinal=%d PID=%d: %w", agent.Int(), pid, ErrStressIdentity)
	}
	return StressProcessIdentity{Kind: StressProcessAgent, Agent: agent, PID: pid}, nil
}

func (identity StressProcessIdentity) key() string {
	return fmt.Sprintf("%s:%d", identity.Kind, identity.Agent.Int())
}
