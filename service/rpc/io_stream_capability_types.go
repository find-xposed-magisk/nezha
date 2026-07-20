package rpc

import (
	"encoding/base64"
	"errors"
)

var (
	ErrAgentCompatCapabilityUnavailable    = errors.New("agentcompat IOStream capability unavailable")
	ErrAgentCompatCapabilityHidden         = errors.New("agentcompat IOStream capability unavailable")
	ErrAgentCompatCapabilityConflict       = errors.New("agentcompat IOStream capability conflict")
	ErrAgentCompatCapabilityBound          = errors.New("agentcompat IOStream capability has a live bound stream")
	ErrAgentCompatCapabilityTokenExhausted = errors.New("agentcompat IOStream capability token attempts exhausted")
)

type AgentCompatCapabilityPurpose uint8

const (
	AgentCompatCapabilityTerminal AgentCompatCapabilityPurpose = iota + 1
	AgentCompatCapabilityFileManager
	AgentCompatCapabilityNAT
)

func (purpose AgentCompatCapabilityPurpose) streamPurpose() StreamPurpose {
	switch purpose {
	case AgentCompatCapabilityTerminal:
		return PurposeTerminal
	case AgentCompatCapabilityFileManager:
		return PurposeFileManager
	case AgentCompatCapabilityNAT:
		return PurposeNAT
	default:
		return PurposeLegacy
	}
}

type AgentCompatCapabilityOwner struct {
	PATID   uint64
	UserID  uint64
	IsAdmin bool
}

type AgentCompatCapabilityRegistration struct {
	Owner               AgentCompatCapabilityOwner
	Purpose             AgentCompatCapabilityPurpose
	TargetServerID      uint64
	ResourceID          uint64
	ServerAccessAllowed bool
}

type AgentCompatIOStreamCapability struct{ value string }

func (capability AgentCompatIOStreamCapability) String() string { return capability.value }

func ParseAgentCompatIOStreamCapability(value string) (AgentCompatIOStreamCapability, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return AgentCompatIOStreamCapability{}, ErrAgentCompatCapabilityHidden
	}
	return AgentCompatIOStreamCapability{value: value}, nil
}

type AgentCompatCapabilityAccess struct {
	Capability          AgentCompatIOStreamCapability
	Owner               AgentCompatCapabilityOwner
	Purpose             AgentCompatCapabilityPurpose
	TargetServerID      uint64
	ResourceID          uint64
	ServerAccessAllowed bool
}

type AgentCompatCapabilityBinding struct {
	AgentCompatCapabilityAccess
	StreamID string
}

type AgentCompatNATPublishHandle struct {
	registration *agentCompatCapabilityRegistration
	generation   uint64
	capability   string
}

type AgentCompatNATStreamLease struct {
	streamID string
	stream   *ioStreamContext
}

type AgentCompatNATPublication struct {
	Purpose        AgentCompatCapabilityPurpose
	TargetServerID uint64
	ResourceID     uint64
	StreamID       string
}
