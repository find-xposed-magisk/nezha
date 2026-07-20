//go:build agentcompat

package rpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatNATHandleCreationBindsEachHandleToItsOwnStream(t *testing.T) {
	handler := NewNezhaHandler()
	firstRegistration := capabilityRegistration(capabilityOwner(501, 502), AgentCompatCapabilityNAT, 503, 504)
	secondRegistration := capabilityRegistration(capabilityOwner(505, 506), AgentCompatCapabilityNAT, 503, 507)
	firstCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), firstRegistration)
	require.NoError(t, err)
	secondCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), secondRegistration)
	require.NoError(t, err)
	firstHandle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(firstCapability, firstRegistration))
	require.NoError(t, err)
	secondHandle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(secondCapability, secondRegistration))
	require.NoError(t, err)

	_, err = handler.CreateAgentCompatNATStream(firstHandle, "handle-bound-first")
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(secondHandle, "handle-bound-second")
	require.NoError(t, err)
	beforeCrossPublish := snapshotAgentCompatNATCreationState(handler, firstRegistration.Owner.PATID, secondRegistration.Owner.PATID)

	require.ErrorIs(t, handler.PublishAgentCompatNATStream(firstHandle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 503, ResourceID: 504, StreamID: "handle-bound-second",
	}), ErrAgentCompatCapabilityHidden)
	require.ErrorIs(t, handler.PublishAgentCompatNATStream(secondHandle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 503, ResourceID: 507, StreamID: "handle-bound-first",
	}), ErrAgentCompatCapabilityHidden)
	requireUnchangedAgentCompatNATCreationState(t, handler, beforeCrossPublish)
	requireNATHandleBindingsIntact(t, handler, firstHandle, "handle-bound-first")
	requireNATHandleBindingsIntact(t, handler, secondHandle, "handle-bound-second")
	require.NoError(t, handler.PublishAgentCompatNATStream(firstHandle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 503, ResourceID: 504, StreamID: "handle-bound-first",
	}))
	require.NoError(t, handler.PublishAgentCompatNATStream(secondHandle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 503, ResourceID: 507, StreamID: "handle-bound-second",
	}))
	firstLease, err := handler.CreateAgentCompatNATStream(firstHandle, "handle-bound-again")
	require.Nil(t, firstLease)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
	require.Equal(t, 2, handler.StreamCount())
}

func TestAgentCompatNATHandleCreationCancelReleasesOnlyItsBoundStream(t *testing.T) {
	handler := NewNezhaHandler()
	firstRegistration := capabilityRegistration(capabilityOwner(508, 509), AgentCompatCapabilityNAT, 510, 511)
	secondRegistration := capabilityRegistration(capabilityOwner(512, 513), AgentCompatCapabilityNAT, 510, 514)
	firstCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), firstRegistration)
	require.NoError(t, err)
	secondCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), secondRegistration)
	require.NoError(t, err)
	firstAccess := capabilityAccess(firstCapability, firstRegistration)
	secondAccess := capabilityAccess(secondCapability, secondRegistration)
	firstHandle, err := handler.ConsumeAgentCompatNATCapability(firstAccess)
	require.NoError(t, err)
	secondHandle, err := handler.ConsumeAgentCompatNATCapability(secondAccess)
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(firstHandle, "bound-first")
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(secondHandle, "bound-second")
	require.NoError(t, err)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(firstAccess))
	_, firstFound := handler.StreamOwnership("bound-first")
	_, secondFound := handler.StreamOwnership("bound-second")
	require.False(t, firstFound)
	require.True(t, secondFound)
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(secondAccess))
}
