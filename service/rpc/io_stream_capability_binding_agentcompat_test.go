//go:build agentcompat

package rpc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatCapabilityCancelLostCreateResponseDeletesOnlyExactStream(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "lost-response", 61)
	require.NoError(t, handler.CreateStreamWithPurpose("other-stream", 21, 61, PurposeTerminal))
	start := handler.SnapshotIOStreamState()

	err := handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration))

	require.NoError(t, err)
	state := handler.SnapshotIOStreamState()
	require.Equal(t, start.Generation+1, state.Generation)
	require.Equal(t, 1, state.Count)
	_, found := handler.StreamOwnership("other-stream")
	require.True(t, found)
}

func TestAgentCompatCapabilityCancelOneOfConcurrentCapabilitiesKeepsOthers(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(12, 22), AgentCompatCapabilityTerminal, 62, 0)
	first, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	second, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	for streamID, capability := range map[string]AgentCompatIOStreamCapability{"first": first, "second": second} {
		require.NoError(t, handler.CreateStreamWithPurpose(streamID, 22, 62, PurposeTerminal))
		require.NoError(t, handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{
			AgentCompatCapabilityAccess: capabilityAccess(capability, registration), StreamID: streamID,
		}))
	}

	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(first, registration)))

	streamID, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(second, registration))
	require.NoError(t, err)
	require.Equal(t, "second", streamID)
}

func TestAgentCompatCapabilityBindValidatesStoredIdentityAndStream(t *testing.T) {
	tests := []struct {
		name          string
		mutateAccess  func(*AgentCompatCapabilityAccess)
		streamOwner   uint64
		streamServer  uint64
		streamPurpose StreamPurpose
	}{
		{name: "foreign PAT", mutateAccess: func(access *AgentCompatCapabilityAccess) { access.Owner.PATID++ }, streamOwner: 23, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "user mismatch", mutateAccess: func(access *AgentCompatCapabilityAccess) { access.Owner.UserID++ }, streamOwner: 23, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "admin mismatch", mutateAccess: func(access *AgentCompatCapabilityAccess) { access.Owner.IsAdmin = true }, streamOwner: 23, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "purpose mismatch", mutateAccess: func(access *AgentCompatCapabilityAccess) { access.Purpose = AgentCompatCapabilityFileManager }, streamOwner: 23, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "target mismatch", mutateAccess: func(access *AgentCompatCapabilityAccess) { access.TargetServerID++ }, streamOwner: 23, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "resource mismatch", mutateAccess: func(access *AgentCompatCapabilityAccess) { access.ResourceID++ }, streamOwner: 23, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "access denied", mutateAccess: func(access *AgentCompatCapabilityAccess) { access.ServerAccessAllowed = false }, streamOwner: 23, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "stream creator mismatch", mutateAccess: func(*AgentCompatCapabilityAccess) {}, streamOwner: 24, streamServer: 63, streamPurpose: PurposeTerminal},
		{name: "stream server mismatch", mutateAccess: func(*AgentCompatCapabilityAccess) {}, streamOwner: 23, streamServer: 64, streamPurpose: PurposeTerminal},
		{name: "stream purpose mismatch", mutateAccess: func(*AgentCompatCapabilityAccess) {}, streamOwner: 23, streamServer: 63, streamPurpose: PurposeFileManager},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			handler := NewNezhaHandler()
			registration := capabilityRegistration(capabilityOwner(13, 23), AgentCompatCapabilityTerminal, 63, 0)
			capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
			require.NoError(t, err)
			require.NoError(t, handler.CreateStreamWithPurpose("candidate", testCase.streamOwner, testCase.streamServer, testCase.streamPurpose))
			access := capabilityAccess(capability, registration)
			testCase.mutateAccess(&access)

			err = handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: access, StreamID: "candidate"})

			require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
		})
	}
}

func TestAgentCompatCapabilityBindIsIdempotentButRejectsConflict(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "original", 64)
	binding := AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: capabilityAccess(capability, registration), StreamID: "original"}
	require.NoError(t, handler.BindAgentCompatIOStreamCapability(binding))
	require.NoError(t, handler.CreateStreamWithPurpose("conflict", 21, 64, PurposeTerminal))
	binding.StreamID = "conflict"

	err := handler.BindAgentCompatIOStreamCapability(binding)

	require.ErrorIs(t, err, ErrAgentCompatCapabilityConflict)
	streamID, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
	require.NoError(t, err)
	require.Equal(t, "original", streamID)
}

func TestAgentCompatCapabilityCancelMismatchOrReplacementDoesNotDetach(t *testing.T) {
	t.Run("target mismatch", func(t *testing.T) {
		handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "target-mismatch", 65)
		start := handler.SnapshotIOStreamState()
		access := capabilityAccess(capability, registration)
		access.TargetServerID++

		err := handler.CancelAgentCompatIOStreamCapability(access)

		require.NoError(t, err)
		require.Equal(t, start, handler.SnapshotIOStreamState())
	})
	t.Run("entry replacement", func(t *testing.T) {
		handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "replaced", 66)
		require.NoError(t, handler.CloseStream("replaced"))
		require.NoError(t, handler.CreateStreamWithPurpose("replaced", 21, 66, PurposeTerminal))
		start := handler.SnapshotIOStreamState()

		err := handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration))

		require.NoError(t, err)
		require.Equal(t, start, handler.SnapshotIOStreamState())
	})
}

func TestAgentCompatCapabilityCancelIsIdentityHidingIdempotentForAbsentAndUnbound(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(14, 24), AgentCompatCapabilityTerminal, 67, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	start := handler.SnapshotIOStreamState()

	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(AgentCompatCapabilityAccess{}))
	require.Equal(t, start, handler.SnapshotIOStreamState())
}

func TestAgentCompatCapabilityCancelAfterNormalCloseIsIdempotent(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "normally-closed", 69)
	require.NoError(t, handler.CloseStream("normally-closed"))
	start := handler.SnapshotIOStreamState()

	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))

	require.Equal(t, start, handler.SnapshotIOStreamState())
}

func TestAgentCompatCapabilityForeignCancelDoesNotMutate(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "foreign-cancel", 70)
	start := handler.SnapshotIOStreamState()
	access := capabilityAccess(capability, registration)
	access.Owner.PATID++

	err := handler.CancelAgentCompatIOStreamCapability(access)

	require.NoError(t, err)
	require.Equal(t, start, handler.SnapshotIOStreamState())
	_, found := handler.StreamOwnership("foreign-cancel")
	require.True(t, found)
}

func TestAgentCompatCapabilityUnregisterRejectsBoundLiveStream(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "bound-unregister", 68)
	start := handler.SnapshotIOStreamState()

	err := handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration))

	require.ErrorIs(t, err, ErrAgentCompatCapabilityBound)
	require.Equal(t, start, handler.SnapshotIOStreamState())
}

func TestAgentCompatCapabilityUnregisterRequiresSamePATAndIsIdempotent(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(15, 25), AgentCompatCapabilityTerminal, 71, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	foreign := capabilityAccess(capability, registration)
	foreign.Owner.PATID++

	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(foreign))
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
}

func TestAgentCompatCapabilityTokenSourceErrorIsVisible(t *testing.T) {
	handler := NewNezhaHandler()
	sourceErr := errors.New("token source failed")
	handler.setAgentCompatCapabilityTokenSourceForTest(func([]byte) error { return sourceErr })

	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityRegistration(capabilityOwner(1, 2), AgentCompatCapabilityTerminal, 3, 0))

	require.ErrorIs(t, err, sourceErr)
}
