//go:build agentcompat

package rpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func natCapabilityFixture(t *testing.T, patID, userID, serverID, profileID uint64) (*NezhaHandler, AgentCompatCapabilityRegistration, AgentCompatIOStreamCapability) {
	t.Helper()
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(patID, userID), AgentCompatCapabilityNAT, serverID, profileID)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	return handler, registration, capability
}

func TestAgentCompatNATCapabilityTransitionsAndRetainsFirstPublication(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 21, 31, 71, 81)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	_, err = handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
	_, err = handler.CreateAgentCompatNATStream(handle, "nat-first")
	require.NoError(t, err)
	require.NoError(t, handler.CreateStreamWithPurpose("nat-second", 0, 71, PurposeNAT))
	publication := AgentCompatNATPublication{Purpose: AgentCompatCapabilityNAT, TargetServerID: 71, ResourceID: 81, StreamID: "nat-first"}
	require.NoError(t, handler.PublishAgentCompatNATStream(handle, publication))
	publication.StreamID = "nat-second"
	require.NoError(t, handler.PublishAgentCompatNATStream(handle, publication))

	streamID, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
	require.NoError(t, err)
	require.Equal(t, "nat-first", streamID)
}

func TestAgentCompatNATCapabilityPublicationBeforeWaitWorks(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 22, 32, 72, 82)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(handle, "nat-published")
	require.NoError(t, err)
	require.NoError(t, handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 72, ResourceID: 82, StreamID: "nat-published",
	}))

	streamID, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
	require.NoError(t, err)
	require.Equal(t, "nat-published", streamID)
}

func TestAgentCompatNATCapabilityValidatesConsumeAndPublishIdentity(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 23, 33, 73, 83)
	access := capabilityAccess(capability, registration)
	access.ResourceID++
	_, err := handler.ConsumeAgentCompatNATCapability(access)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(handle, "nat-identity")
	require.NoError(t, err)

	err = handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 73, ResourceID: 84, StreamID: "nat-identity",
	})

	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
}

func TestAgentCompatNATCapabilityLatePublishAfterUnregisterIsIgnored(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 24, 34, 74, 84)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	require.NoError(t, handler.CreateStreamWithPurpose("nat-late", 0, 74, PurposeNAT))

	err = handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 74, ResourceID: 84, StreamID: "nat-late",
	})

	require.NoError(t, err)
	_, err = handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
}

func TestAgentCompatNATCapabilityLatePublishAfterCancelIsIgnored(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 27, 37, 77, 87)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	require.NoError(t, handler.CreateStreamWithPurpose("nat-after-cancel", 0, 77, PurposeNAT))

	err = handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 77, ResourceID: 87, StreamID: "nat-after-cancel",
	})

	require.NoError(t, err)
	_, found := handler.StreamOwnership("nat-after-cancel")
	require.True(t, found)
}

func TestAgentCompatNATCapabilityReusedTokenCannotBindAnotherStream(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 28, 38, 78, 88)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(handle, "nat-original")
	require.NoError(t, err)
	require.NoError(t, handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 78, ResourceID: 88, StreamID: "nat-original",
	}))
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	require.NoError(t, handler.CreateStreamWithPurpose("nat-reuse", 0, 78, PurposeNAT))

	_, err = handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
	require.NoError(t, handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 78, ResourceID: 88, StreamID: "nat-reuse",
	}))
	_, found := handler.StreamOwnership("nat-reuse")
	require.True(t, found)
}

func TestAgentCompatNATCapabilityCancelDetachesPublishedStream(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 25, 35, 75, 85)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(handle, "nat-cancel")
	require.NoError(t, err)
	require.NoError(t, handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 75, ResourceID: 85, StreamID: "nat-cancel",
	}))
	start := handler.SnapshotIOStreamState()

	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))

	state := handler.SnapshotIOStreamState()
	require.Equal(t, start.Generation+1, state.Generation)
	require.Equal(t, 0, state.Count)
}

func TestAgentCompatNATCapabilitiesRemainSeparatedAcrossProfiles(t *testing.T) {
	handler := NewNezhaHandler()
	owner := capabilityOwner(26, 36)
	firstRegistration := capabilityRegistration(owner, AgentCompatCapabilityNAT, 76, 86)
	secondRegistration := capabilityRegistration(owner, AgentCompatCapabilityNAT, 76, 87)
	first, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), firstRegistration)
	require.NoError(t, err)
	second, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), secondRegistration)
	require.NoError(t, err)
	firstHandle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(first, firstRegistration))
	require.NoError(t, err)
	secondHandle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(second, secondRegistration))
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(firstHandle, "nat-profile-first")
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(secondHandle, "nat-profile-second")
	require.NoError(t, err)
	require.NoError(t, handler.PublishAgentCompatNATStream(firstHandle, AgentCompatNATPublication{Purpose: AgentCompatCapabilityNAT, TargetServerID: 76, ResourceID: 86, StreamID: "nat-profile-first"}))
	require.NoError(t, handler.PublishAgentCompatNATStream(secondHandle, AgentCompatNATPublication{Purpose: AgentCompatCapabilityNAT, TargetServerID: 76, ResourceID: 87, StreamID: "nat-profile-second"}))

	firstStream, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(first, firstRegistration))
	require.NoError(t, err)
	secondStream, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(second, secondRegistration))
	require.NoError(t, err)
	require.Equal(t, "nat-profile-first", firstStream)
	require.Equal(t, "nat-profile-second", secondStream)
}
