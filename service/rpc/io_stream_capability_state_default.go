//go:build !agentcompat

package rpc

type agentCompatCapabilityState struct{}

type agentCompatCapabilityRegistration struct{}

func (*NezhaHandler) initializeAgentCompatCapabilities() {}
