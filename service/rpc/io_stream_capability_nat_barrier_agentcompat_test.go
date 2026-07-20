//go:build agentcompat

package rpc

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatNATCapabilityUnregisterBarrierMakesQueuedPublishInert(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 38, 48, 60, 70)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(handle, "nat-barrier")
	require.NoError(t, err)
	require.NoError(t, handler.detachExactStream("nat-barrier", handle.registration.stream))
	stateBeforeRace := handler.SnapshotIOStreamState()

	publishEntered := make(chan struct{})
	publishRelease := make(chan struct{})
	publishObserverCtx := agentCompatCapabilityTestContext(t)
	var observeOnce sync.Once
	handler.setAgentCompatCapabilityPublishObserverForTest(func() {
		observeOnce.Do(func() {
			close(publishEntered)
			select {
			case <-publishRelease:
			case <-publishObserverCtx.Done():
			}
		})
	})
	t.Cleanup(func() { handler.setAgentCompatCapabilityPublishObserverForTest(nil) })
	publishResult := make(chan error, 1)
	go func() {
		publishResult <- handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
			Purpose: AgentCompatCapabilityNAT, TargetServerID: 60, ResourceID: 70, StreamID: "nat-barrier",
		})
	}()
	awaitAgentCompatCapabilitySignal(t, publishEntered, "publish did not enter production path before unregister")

	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
	close(publishRelease)
	require.NoError(t, receiveAgentCompatCapabilityError(t, publishResult, "queued publish did not return after release"))
	_, err = handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
	require.Equal(t, stateBeforeRace, handler.SnapshotIOStreamState())
	_, found := handler.StreamOwnership("nat-barrier")
	require.False(t, found)
	handler.ioStreamMutex.RLock()
	_, active := handler.agentCompatCapabilities.active[capability.value]
	retainedStreamID := handle.registration.streamID
	retainedStream := handle.registration.stream
	handler.ioStreamMutex.RUnlock()
	require.False(t, active)
	require.Equal(t, "nat-barrier", retainedStreamID)
	require.NotNil(t, retainedStream)
}

func TestAgentCompatNATCapabilityPublishObserverCanReenterRegistry(t *testing.T) {
	handler, registration, capability := natCapabilityFixture(t, 39, 49, 61, 71)
	handle, err := handler.ConsumeAgentCompatNATCapability(capabilityAccess(capability, registration))
	require.NoError(t, err)
	_, err = handler.CreateAgentCompatNATStream(handle, "nat-observer-reentry")
	require.NoError(t, err)
	observerEntered := make(chan struct{})
	var observeOnce sync.Once
	handler.setAgentCompatCapabilityPublishObserverForTest(func() {
		handler.SnapshotIOStreamState()
		observeOnce.Do(func() { close(observerEntered) })
	})
	t.Cleanup(func() { handler.setAgentCompatCapabilityPublishObserverForTest(nil) })
	publishResult := make(chan error, 1)
	go func() {
		publishResult <- handler.PublishAgentCompatNATStream(handle, AgentCompatNATPublication{
			Purpose: AgentCompatCapabilityNAT, TargetServerID: 61, ResourceID: 71, StreamID: "nat-observer-reentry",
		})
	}()

	awaitAgentCompatCapabilitySignal(t, observerEntered, "publish observer did not reenter registry")
	require.NoError(t, receiveAgentCompatCapabilityError(t, publishResult, "publish observer reentry deadlocked"))
	streamID, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
	require.NoError(t, err)
	require.Equal(t, "nat-observer-reentry", streamID)
}

func TestAgentCompatNATCapabilityPublishObserverIsHandlerScoped(t *testing.T) {
	first, firstRegistration, firstCapability := natCapabilityFixture(t, 40, 50, 62, 72)
	second, secondRegistration, secondCapability := natCapabilityFixture(t, 41, 51, 63, 73)
	firstHandle, err := first.ConsumeAgentCompatNATCapability(capabilityAccess(firstCapability, firstRegistration))
	require.NoError(t, err)
	secondHandle, err := second.ConsumeAgentCompatNATCapability(capabilityAccess(secondCapability, secondRegistration))
	require.NoError(t, err)
	_, err = first.CreateAgentCompatNATStream(firstHandle, "nat-scoped-first")
	require.NoError(t, err)
	_, err = second.CreateAgentCompatNATStream(secondHandle, "nat-scoped-second")
	require.NoError(t, err)
	firstObserved := make(chan struct{})
	secondObserved := make(chan struct{})
	first.setAgentCompatCapabilityPublishObserverForTest(func() { close(firstObserved) })
	second.setAgentCompatCapabilityPublishObserverForTest(func() { close(secondObserved) })

	require.NoError(t, first.PublishAgentCompatNATStream(firstHandle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 62, ResourceID: 72, StreamID: "nat-scoped-first",
	}))
	awaitAgentCompatCapabilitySignal(t, firstObserved, "first handler observer did not run")
	select {
	case <-secondObserved:
		t.Fatal("second handler observer ran for first handler publish")
	default:
	}
	require.NoError(t, second.PublishAgentCompatNATStream(secondHandle, AgentCompatNATPublication{
		Purpose: AgentCompatCapabilityNAT, TargetServerID: 63, ResourceID: 73, StreamID: "nat-scoped-second",
	}))
	awaitAgentCompatCapabilitySignal(t, secondObserved, "second handler observer did not run")
}
