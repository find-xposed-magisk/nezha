//go:build agentcompat

package rpc

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatNATCapabilityForProfileConsumesStoredRegistration(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 61, 71, 81, 91)

	access, handle, err := handler.ConsumeAgentCompatNATCapabilityForProfile(capability.String(), 81, 91)

	require.NoError(t, err)
	require.Equal(t, registration.Owner, access.Owner)
	require.Equal(t, registration.Purpose, access.Purpose)
	require.Equal(t, registration.TargetServerID, access.TargetServerID)
	require.Equal(t, registration.ResourceID, access.ResourceID)
	require.True(t, access.ServerAccessAllowed)
	require.NotEmpty(t, handle.capability)
}

func TestAgentCompatNATCapabilityForProfileHidesMalformedUnknownAndForeignTuples(t *testing.T) {
	handler, _, capability := natCapabilityFixture(t, 62, 72, 82, 92)
	terminalRegistration := capabilityRegistration(capabilityOwner(66, 76), AgentCompatCapabilityTerminal, 82, 0)
	terminalCapability := registerAgentCompatCapability(t, handler, terminalRegistration)
	cases := []struct {
		name       string
		value      string
		serverID   uint64
		resourceID uint64
	}{
		{name: "malformed", value: "not-a-capability", serverID: 82, resourceID: 92},
		{name: "unknown", value: strings.Repeat("a", 43), serverID: 82, resourceID: 92},
		{name: "wrong server", value: capability.String(), serverID: 83, resourceID: 92},
		{name: "wrong profile", value: capability.String(), serverID: 82, resourceID: 93},
		{name: "wrong purpose", value: terminalCapability.String(), serverID: 82, resourceID: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := handler.ConsumeAgentCompatNATCapabilityForProfile(testCase.value, testCase.serverID, testCase.resourceID)
			require.True(t, errors.Is(err, ErrAgentCompatCapabilityHidden))
			require.NotContains(t, err.Error(), testCase.value)
		})
	}
}

func TestAgentCompatNATCapabilityForProfileHidesRepeatedAndInactiveConsume(t *testing.T) {
	handler, _, capability := natCapabilityFixture(t, 63, 73, 83, 93)
	activeBefore, usedBefore := agentCompatCapabilityRegistryCounts(handler)
	_, _, err := handler.ConsumeAgentCompatNATCapabilityForProfile(capability.String(), 83, 93)
	require.NoError(t, err)
	activeAfter, usedAfter := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, activeBefore, activeAfter)
	require.Equal(t, usedBefore, usedAfter)

	_, _, err = handler.ConsumeAgentCompatNATCapabilityForProfile(capability.String(), 83, 93)
	require.True(t, errors.Is(err, ErrAgentCompatCapabilityHidden))
	activeAfterRepeat, usedAfterRepeat := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, activeAfter, activeAfterRepeat)
	require.Equal(t, usedAfter, usedAfterRepeat)

	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(AgentCompatCapabilityAccess{}))
}

func TestAgentCompatNATCapabilityForProfileHidesCancelledAndUnregistered(t *testing.T) {
	tests := []struct {
		name    string
		cleanup func(*NezhaHandler, AgentCompatCapabilityAccess) error
	}{
		{name: "cancelled", cleanup: (*NezhaHandler).CancelAgentCompatIOStreamCapability},
		{name: "unregistered", cleanup: (*NezhaHandler).UnregisterAgentCompatIOStreamCapability},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			handler, registration, capability := natCapabilityFixture(t, 64, 74, 84, 94)
			access := capabilityAccess(capability, registration)
			require.NoError(t, testCase.cleanup(handler, access))

			_, _, err := handler.ConsumeAgentCompatNATCapabilityForProfile(capability.String(), 84, 94)
			require.True(t, errors.Is(err, ErrAgentCompatCapabilityHidden))
		})
	}
}

func TestAgentCompatNATCapabilityForProfileDoesNotLeakSensitiveValues(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 65, 75, 85, 95)
	_, _, err := handler.ConsumeAgentCompatNATCapabilityForProfile(capability.String(), 86, 95)
	require.Error(t, err)
	message := err.Error()
	for _, sensitive := range []string{capability.String(), "65", "75", "85", "95", "nat"} {
		require.NotContains(t, message, sensitive)
	}
	require.Equal(t, AgentCompatCapabilityNAT, registration.Purpose)
}
