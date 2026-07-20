//go:build !agentcompat

package rpc

import "context"

func (*NezhaHandler) RegisterAgentCompatIOStreamCapability(context.Context, AgentCompatCapabilityRegistration) (AgentCompatIOStreamCapability, error) {
	return AgentCompatIOStreamCapability{}, ErrAgentCompatCapabilityUnavailable
}

func (*NezhaHandler) BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding) error {
	return ErrAgentCompatCapabilityUnavailable
}

func (*NezhaHandler) ConsumeAgentCompatNATCapability(AgentCompatCapabilityAccess) (AgentCompatNATPublishHandle, error) {
	return AgentCompatNATPublishHandle{}, ErrAgentCompatCapabilityUnavailable
}

func (*NezhaHandler) ConsumeAgentCompatNATCapabilityForProfile(string, uint64, uint64) (AgentCompatCapabilityAccess, AgentCompatNATPublishHandle, error) {
	return AgentCompatCapabilityAccess{}, AgentCompatNATPublishHandle{}, ErrAgentCompatCapabilityUnavailable
}

func (*NezhaHandler) PublishAgentCompatNATStream(AgentCompatNATPublishHandle, AgentCompatNATPublication) error {
	return ErrAgentCompatCapabilityUnavailable
}

func (*NezhaHandler) WaitAgentCompatIOStreamCapability(context.Context, AgentCompatCapabilityAccess) (string, error) {
	return "", ErrAgentCompatCapabilityUnavailable
}

func (*NezhaHandler) CancelAgentCompatIOStreamCapability(AgentCompatCapabilityAccess) error {
	return nil
}

func (*NezhaHandler) UnregisterAgentCompatIOStreamCapability(AgentCompatCapabilityAccess) error {
	return nil
}

func (*NezhaHandler) CreateAgentCompatNATStream(AgentCompatNATPublishHandle, string) (*AgentCompatNATStreamLease, error) {
	return nil, ErrAgentCompatCapabilityUnavailable
}

func (*NezhaHandler) CloseAgentCompatNATStreamLease(*AgentCompatNATStreamLease) error {
	return nil
}
