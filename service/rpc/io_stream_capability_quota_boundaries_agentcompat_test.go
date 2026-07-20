//go:build agentcompat

package rpc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatCapabilityRegistrationEnforcesPerPATActiveQuotaAndReusesReleasedSlot(t *testing.T) {
	handler := NewNezhaHandler()
	issued := setUniqueAgentCompatCapabilityTokens(handler)
	registration := capabilityRegistration(capabilityOwner(101, 201), AgentCompatCapabilityTerminal, 301, 0)
	capabilities := make([]AgentCompatIOStreamCapability, 0, 16)
	for range 16 {
		capabilities = append(capabilities, registerAgentCompatCapability(t, handler, registration))
	}

	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	require.Equal(t, uint64(16), issued.Load())
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capabilities[0], registration)))

	replacement := registerAgentCompatCapability(t, handler, registration)
	require.NotEmpty(t, replacement.String())
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 16, active)
	require.Equal(t, 17, used)
}

func TestAgentCompatCapabilityRegistrationEnforcesGlobalActiveQuotaAndReusesReleasedSlot(t *testing.T) {
	handler := NewNezhaHandler()
	issued := setUniqueAgentCompatCapabilityTokens(handler)
	registrations := make([]AgentCompatCapabilityRegistration, 0, 128)
	capabilities := make([]AgentCompatIOStreamCapability, 0, 128)
	for index := range 128 {
		registration := capabilityRegistration(capabilityOwner(uint64(index+1), uint64(index+1001)), AgentCompatCapabilityTerminal, 302, 0)
		registrations = append(registrations, registration)
		capabilities = append(capabilities, registerAgentCompatCapability(t, handler, registration))
	}
	overflow := capabilityRegistration(capabilityOwner(10000, 20000), AgentCompatCapabilityTerminal, 302, 0)

	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), overflow)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	require.Equal(t, uint64(128), issued.Load())
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capabilities[0], registrations[0])))

	replacement := registerAgentCompatCapability(t, handler, overflow)
	require.NotEmpty(t, replacement.String())
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 128, active)
	require.Equal(t, 129, used)
}

func TestAgentCompatCapabilityRegistrationEnforcesProcessLifetimeMintQuota(t *testing.T) {
	handler := NewNezhaHandler()
	issued := setUniqueAgentCompatCapabilityTokens(handler)
	registration := capabilityRegistration(capabilityOwner(102, 202), AgentCompatCapabilityTerminal, 303, 0)
	for range 4096 {
		capability := registerAgentCompatCapability(t, handler, registration)
		require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	}

	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)

	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	require.Equal(t, uint64(4096), issued.Load())
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Zero(t, active)
	require.Equal(t, 4096, used)
}

func TestAgentCompatCapabilityCollisionRetriesDoNotConsumeQuota(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(103, 203), AgentCompatCapabilityTerminal, 304, 0)
	fixedToken := make([]byte, 32)
	fixedToken[0] = 1
	handler.setAgentCompatCapabilityTokenSourceForTest(func(destination []byte) error {
		copy(destination, fixedToken)
		return nil
	})
	first := registerAgentCompatCapability(t, handler, registration)
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(first, registration)))

	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)

	require.ErrorIs(t, err, ErrAgentCompatCapabilityTokenExhausted)
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Zero(t, active)
	require.Equal(t, 1, used)
	require.False(t, errors.Is(err, ErrAgentCompatCapabilityUnavailable))
}
