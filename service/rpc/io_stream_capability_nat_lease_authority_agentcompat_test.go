//go:build agentcompat

package rpc

import (
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type agentCompatNATCreationState struct {
	streamState IOStreamState
	active      int
	used        int
	patState    map[uint64]agentCompatNATPATState
}

type agentCompatNATPATState struct {
	active uint16
	exists bool
}

func snapshotAgentCompatNATCreationState(handler *NezhaHandler, patIDs ...uint64) agentCompatNATCreationState {
	active, used := agentCompatCapabilityRegistryCounts(handler)
	patState := make(map[uint64]agentCompatNATPATState, len(patIDs))
	for _, patID := range patIDs {
		patActive, patExists := agentCompatCapabilityActiveForPAT(handler, patID)
		patState[patID] = agentCompatNATPATState{active: patActive, exists: patExists}
	}
	return agentCompatNATCreationState{
		streamState: handler.SnapshotIOStreamState(),
		active:      active,
		used:        used,
		patState:    patState,
	}
}

func requireUnchangedAgentCompatNATCreationState(t *testing.T, handler *NezhaHandler, before agentCompatNATCreationState) {
	t.Helper()
	ids := make([]uint64, 0, len(before.patState))
	for patID := range before.patState {
		ids = append(ids, patID)
	}
	after := snapshotAgentCompatNATCreationState(handler, ids...)
	require.Equal(t, before, after)
}

func requireHiddenCreateAgentCompatNATStream(t *testing.T, handler *NezhaHandler, handle AgentCompatNATPublishHandle, streamID string, before agentCompatNATCreationState) {
	t.Helper()
	lease, err := handler.CreateAgentCompatNATStream(handle, streamID)
	require.Nil(t, lease)
	require.True(t, errors.Is(err, ErrAgentCompatCapabilityHidden) || errors.Is(err, ErrAgentCompatCapabilityUnavailable))
	requireUnchangedAgentCompatNATCreationState(t, handler, before)
}

func requireNATHandleBindingsIntact(t *testing.T, handler *NezhaHandler, handle AgentCompatNATPublishHandle, streamID string) {
	t.Helper()
	handler.ioStreamMutex.RLock()
	defer handler.ioStreamMutex.RUnlock()
	registration := handle.registration
	require.NotNil(t, registration)
	require.Equal(t, agentCompatCapabilityConsumed, registration.phase)
	require.Equal(t, streamID, registration.streamID)
	stream, exists := handler.ioStreams[streamID]
	require.True(t, exists)
	require.Same(t, stream, registration.stream)
}

func TestAgentCompatNATHandleCreationAuthorityIsBoundToExactRegistration(t *testing.T) {
	handler := NewNezhaHandler()
	firstRegistration := capabilityRegistration(capabilityOwner(601, 602), AgentCompatCapabilityNAT, 603, 604)
	secondRegistration := capabilityRegistration(capabilityOwner(605, 606), AgentCompatCapabilityNAT, 603, 607)
	firstCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), firstRegistration)
	if err != nil {
		t.Fatal("first capability registration failed")
	}
	secondCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), secondRegistration)
	if err != nil {
		t.Fatal("second capability registration failed")
	}
	firstHandle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(firstCapability, firstRegistration))
	if err != nil {
		t.Fatal("first capability consume failed")
	}
	secondHandle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(secondCapability, secondRegistration))
	if err != nil {
		t.Fatal("second capability consume failed")
	}
	firstLease, err := handler.CreateAgentCompatNATStream(firstHandle, "bound-first")
	if err != nil || firstLease == nil {
		t.Fatal("first capability did not create its stream")
	}
	secondLease, err := handler.CreateAgentCompatNATStream(secondHandle, "bound-second")
	if err != nil || secondLease == nil {
		t.Fatal("second capability did not create its stream")
	}
	beforeCrossPublish := snapshotAgentCompatNATCreationState(handler, firstRegistration.Owner.PATID, secondRegistration.Owner.PATID)
	if err := handler.PublishAgentCompatNATStream(firstHandle, AgentCompatNATPublication{Purpose: AgentCompatCapabilityNAT, TargetServerID: 603, ResourceID: 604, StreamID: "bound-second"}); err != ErrAgentCompatCapabilityHidden {
		t.Fatal("first capability published the second stream")
	}
	if err := handler.PublishAgentCompatNATStream(secondHandle, AgentCompatNATPublication{Purpose: AgentCompatCapabilityNAT, TargetServerID: 603, ResourceID: 607, StreamID: "bound-first"}); err != ErrAgentCompatCapabilityHidden {
		t.Fatal("second capability published the first stream")
	}
	requireUnchangedAgentCompatNATCreationState(t, handler, beforeCrossPublish)
	requireNATHandleBindingsIntact(t, handler, firstHandle, "bound-first")
	requireNATHandleBindingsIntact(t, handler, secondHandle, "bound-second")
	require.NoError(t, handler.PublishAgentCompatNATStream(firstHandle, AgentCompatNATPublication{Purpose: AgentCompatCapabilityNAT, TargetServerID: 603, ResourceID: 604, StreamID: "bound-first"}))
	publishedBeforeRepeat := snapshotAgentCompatNATCreationState(handler, firstRegistration.Owner.PATID)
	requireHiddenCreateAgentCompatNATStream(t, handler, firstHandle, "bound-after-publish", publishedBeforeRepeat)
	if lease, err := handler.CreateAgentCompatNATStream(firstHandle, "bound-again"); lease != nil || err != ErrAgentCompatCapabilityHidden {
		t.Fatal("repeated creation mutated first capability state")
	}
	if handler.StreamCount() != 2 {
		t.Fatal("repeated creation changed stream accounting")
	}
	if err := handler.CancelAgentCompatIOStreamCapability(capabilityAccess(firstCapability, firstRegistration)); err != nil {
		t.Fatal("first capability cancellation failed")
	}
	if _, found := handler.StreamOwnership("bound-first"); found {
		t.Fatal("first stream remained after cancellation")
	}
	if _, found := handler.StreamOwnership("bound-second"); !found {
		t.Fatal("second stream was affected by first cancellation")
	}
	if err := handler.CloseAgentCompatNATStreamLease(secondLease); err != nil {
		t.Fatal("second exact lease close failed")
	}
}

func TestAgentCompatNATHandleCreationRejectsInvalidAuthorityWithoutMutation(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 701, 702, 703, 704)
	access := capabilityAccess(capability, registration)
	before := snapshotAgentCompatNATCreationState(handler, registration.Owner.PATID)
	requireHiddenCreateAgentCompatNATStream(t, handler, AgentCompatNATPublishHandle{}, "invalid-zero", before)

	foreignHandler, foreignRegistration, foreignCapability := natCapabilityFixture(t, 705, 706, 703, 707)
	foreignHandle, err := foreignHandler.ConsumeAgentCompatNATCapability(capabilityAccess(foreignCapability, foreignRegistration))
	require.NoError(t, err)
	foreignBefore := snapshotAgentCompatNATCreationState(foreignHandler, foreignRegistration.Owner.PATID)
	requireHiddenCreateAgentCompatNATStream(t, handler, foreignHandle, "invalid-foreign", before)
	requireUnchangedAgentCompatNATCreationState(t, foreignHandler, foreignBefore)

	registeredCapability := registerAgentCompatCapability(t, handler, capabilityRegistration(capabilityOwner(708, 709), AgentCompatCapabilityNAT, 703, 710))
	registeredParsed, err := ParseAgentCompatIOStreamCapability(registeredCapability.String())
	require.NoError(t, err)
	registeredHandle := AgentCompatNATPublishHandle{capability: registeredParsed.value}
	handler.ioStreamMutex.RLock()
	registeredHandle.registration = handler.agentCompatCapabilities.active[registeredParsed.value]
	registeredHandle.generation = registeredHandle.registration.generation
	handler.ioStreamMutex.RUnlock()
	registeredBefore := snapshotAgentCompatNATCreationState(handler, registration.Owner.PATID, 708)
	requireHiddenCreateAgentCompatNATStream(t, handler, registeredHandle, "invalid-registered", registeredBefore)

	staleHandle, err := handler.ConsumeAgentCompatNATCapability(access)
	require.NoError(t, err)
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
	staleBefore := snapshotAgentCompatNATCreationState(handler, registration.Owner.PATID)
	requireHiddenCreateAgentCompatNATStream(t, handler, staleHandle, "invalid-unregistered", staleBefore)

	cancelRegistration := capabilityRegistration(capabilityOwner(711, 712), AgentCompatCapabilityNAT, 703, 713)
	cancelCapability := registerAgentCompatCapability(t, handler, cancelRegistration)
	cancelAccess := capabilityAccess(cancelCapability, cancelRegistration)
	cancelHandle, err := handler.ConsumeAgentCompatNATCapability(cancelAccess)
	require.NoError(t, err)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(cancelAccess))
	cancelledBefore := snapshotAgentCompatNATCreationState(handler, registration.Owner.PATID, cancelRegistration.Owner.PATID)
	requireHiddenCreateAgentCompatNATStream(t, handler, cancelHandle, "invalid-cancelled", cancelledBefore)

	wrongPurposeRegistration := capabilityRegistration(capabilityOwner(714, 715), AgentCompatCapabilityTerminal, 703, 0)
	wrongPurposeCapability := registerAgentCompatCapability(t, handler, wrongPurposeRegistration)
	wrongPurposeParsed, err := ParseAgentCompatIOStreamCapability(wrongPurposeCapability.String())
	require.NoError(t, err)
	handler.ioStreamMutex.RLock()
	wrongPurposeRegistrationState := handler.agentCompatCapabilities.active[wrongPurposeParsed.value]
	handler.ioStreamMutex.RUnlock()
	wrongPurposeHandle := AgentCompatNATPublishHandle{registration: wrongPurposeRegistrationState, generation: wrongPurposeRegistrationState.generation, capability: wrongPurposeParsed.value}
	wrongPurposeBefore := snapshotAgentCompatNATCreationState(handler, registration.Owner.PATID, wrongPurposeRegistration.Owner.PATID)
	requireHiddenCreateAgentCompatNATStream(t, handler, wrongPurposeHandle, "invalid-purpose", wrongPurposeBefore)

}

func TestAgentCompatNATHandleCreationRepeatedCreatePreservesAccountingAndQuota(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 721, 722, 723, 724)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	lease, err := handler.CreateAgentCompatNATStream(handle, "repeated-create-first")
	require.NoError(t, err)
	stateBeforeRepeat := snapshotAgentCompatNATCreationState(handler, registration.Owner.PATID)
	requireHiddenCreateAgentCompatNATStream(t, handler, handle, "repeated-create-second", stateBeforeRepeat)

	for index := 0; index < maxStreamsPerServer-1; index++ {
		require.NoError(t, handler.CreateStreamWithPurpose("quota-boundary-"+strconv.Itoa(index), 0, registration.TargetServerID, PurposeNAT))
	}
	require.ErrorIs(t, handler.CreateStreamWithPurpose("quota-boundary-overflow", 0, registration.TargetServerID, PurposeNAT), ErrTooManyStreamsForServer)
	require.NoError(t, handler.CloseAgentCompatNATStreamLease(lease))
	for index := 0; index < maxStreamsPerServer-1; index++ {
		require.NoError(t, handler.CloseStream("quota-boundary-"+strconv.Itoa(index)))
	}
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
}
