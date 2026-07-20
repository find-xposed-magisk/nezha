//go:build agentcompat

package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatCapabilityRegistrationExhaustsPermanentCollisionWithoutBlockingRegistry(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(31, 41), AgentCompatCapabilityTerminal, 51, 0)
	fixedToken := make([]byte, 32)
	fixedToken[0] = 1
	handler.setAgentCompatCapabilityTokenSourceForTest(func(destination []byte) error {
		copy(destination, fixedToken)
		return nil
	})
	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	releaseCtx := agentCompatCapabilityTestContext(t)
	var once sync.Once
	handler.setAgentCompatCapabilityTokenSourceForTest(func(destination []byte) error {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-releaseCtx.Done():
			}
		})
		copy(destination, fixedToken)
		return nil
	})
	result := make(chan error, 1)
	go func() {
		_, registerErr := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
		result <- registerErr
	}()
	awaitAgentCompatCapabilitySignal(t, entered, "token source did not enter")

	registryRead := make(chan struct{})
	go func() {
		handler.SnapshotIOStreamState()
		close(registryRead)
	}()
	awaitAgentCompatCapabilitySignal(t, registryRead, "token source blocked unrelated registry operation")
	close(release)
	require.ErrorIs(t, receiveAgentCompatCapabilityError(t, result, "permanent token collision did not terminate"), ErrAgentCompatCapabilityTokenExhausted)
}

func TestAgentCompatCapabilityRegistrationPreservesCanceledContext(t *testing.T) {
	handler := NewNezhaHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := handler.RegisterAgentCompatIOStreamCapability(ctx, capabilityRegistration(capabilityOwner(32, 42), AgentCompatCapabilityTerminal, 52, 0))

	require.ErrorIs(t, err, context.Canceled)
}

func TestAgentCompatCapabilityTokenSourceCanReenterRegistry(t *testing.T) {
	handler := NewNezhaHandler()
	handler.setAgentCompatCapabilityTokenSourceForTest(func(destination []byte) error {
		handler.SnapshotIOStreamState()
		destination[0] = 1
		return nil
	})
	result := make(chan error, 1)
	go func() {
		_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityRegistration(capabilityOwner(33, 43), AgentCompatCapabilityTerminal, 53, 0))
		result <- err
	}()

	require.NoError(t, receiveAgentCompatCapabilityError(t, result, "reentrant token source deadlocked"))
}

func TestAgentCompatCapabilityWaitObserverCanReenterRegistry(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(34, 44), AgentCompatCapabilityTerminal, 54, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	access := capabilityAccess(capability, registration)
	handler.setAgentCompatCapabilityWaitObserverForTest(func() {
		require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
	})
	result := make(chan error, 1)
	waitCtx := agentCompatCapabilityTestContext(t)
	go func() {
		_, waitErr := handler.WaitAgentCompatIOStreamCapability(waitCtx, access)
		result <- waitErr
	}()

	require.ErrorIs(t, receiveAgentCompatCapabilityError(t, result, "reentrant wait observer deadlocked"), ErrAgentCompatCapabilityHidden)
}

func TestAgentCompatCapabilityWaitRejectsSameIDReplacement(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "reused-stream-id", 55)
	require.NoError(t, handler.CloseStream("reused-stream-id"))
	require.NoError(t, handler.CreateStreamWithPurpose("reused-stream-id", 21, 55, PurposeTerminal))

	_, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))

	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
}

func TestAgentCompatCapabilityCancelAndUnregisterDoNotEnumerateForeignIdentity(t *testing.T) {
	operations := []struct {
		name string
		run  func(*NezhaHandler, AgentCompatCapabilityAccess) error
	}{
		{name: "cancel", run: (*NezhaHandler).CancelAgentCompatIOStreamCapability},
		{name: "unregister", run: (*NezhaHandler).UnregisterAgentCompatIOStreamCapability},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, operation.name+"-foreign", 56)
			foreign := capabilityAccess(capability, registration)
			foreign.Owner.PATID++
			before := handler.SnapshotIOStreamState()

			foreignErr := operation.run(handler, foreign)
			unknownErr := operation.run(handler, AgentCompatCapabilityAccess{})

			require.NoError(t, foreignErr)
			require.NoError(t, unknownErr)
			require.Equal(t, before, handler.SnapshotIOStreamState())
			_, found := handler.StreamOwnership(operation.name + "-foreign")
			require.True(t, found)
		})
	}
}

func TestAgentCompatCapabilityAccessMismatchMatrixIsHiddenOrInert(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*AgentCompatCapabilityAccess)
	}{
		{name: "PAT", mutate: func(access *AgentCompatCapabilityAccess) { access.Owner.PATID++ }},
		{name: "user", mutate: func(access *AgentCompatCapabilityAccess) { access.Owner.UserID++ }},
		{name: "admin", mutate: func(access *AgentCompatCapabilityAccess) { access.Owner.IsAdmin = !access.Owner.IsAdmin }},
		{name: "purpose", mutate: func(access *AgentCompatCapabilityAccess) { access.Purpose = AgentCompatCapabilityFileManager }},
		{name: "resource", mutate: func(access *AgentCompatCapabilityAccess) { access.ResourceID++ }},
		{name: "server", mutate: func(access *AgentCompatCapabilityAccess) { access.TargetServerID++ }},
		{name: "access proof", mutate: func(access *AgentCompatCapabilityAccess) { access.ServerAccessAllowed = false }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			handler := NewNezhaHandler()
			registration := capabilityRegistration(capabilityOwner(35, 45), AgentCompatCapabilityTerminal, 57, 0)
			capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
			require.NoError(t, err)
			require.NoError(t, handler.CreateStreamWithPurpose("matrix", 45, 57, PurposeTerminal))
			access := capabilityAccess(capability, registration)
			mutation.mutate(&access)

			_, waitErr := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), access)
			bindErr := handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: access, StreamID: "matrix"})
			cancelErr := handler.CancelAgentCompatIOStreamCapability(access)
			unregisterErr := handler.UnregisterAgentCompatIOStreamCapability(access)

			require.ErrorIs(t, waitErr, ErrAgentCompatCapabilityHidden)
			require.ErrorIs(t, bindErr, ErrAgentCompatCapabilityHidden)
			require.NoError(t, cancelErr)
			require.NoError(t, unregisterErr)
			require.NoError(t, handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{
				AgentCompatCapabilityAccess: capabilityAccess(capability, registration), StreamID: "matrix",
			}))
			streamID, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
			require.NoError(t, err)
			require.Equal(t, "matrix", streamID)
		})
	}
}

func TestAgentCompatNATCapabilityConsumeMismatchMatrixIsHidden(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*AgentCompatCapabilityAccess)
	}{
		{name: "PAT", mutate: func(access *AgentCompatCapabilityAccess) { access.Owner.PATID++ }},
		{name: "user", mutate: func(access *AgentCompatCapabilityAccess) { access.Owner.UserID++ }},
		{name: "admin", mutate: func(access *AgentCompatCapabilityAccess) { access.Owner.IsAdmin = !access.Owner.IsAdmin }},
		{name: "purpose", mutate: func(access *AgentCompatCapabilityAccess) { access.Purpose = AgentCompatCapabilityTerminal }},
		{name: "resource", mutate: func(access *AgentCompatCapabilityAccess) { access.ResourceID++ }},
		{name: "server", mutate: func(access *AgentCompatCapabilityAccess) { access.TargetServerID++ }},
		{name: "access proof", mutate: func(access *AgentCompatCapabilityAccess) { access.ServerAccessAllowed = false }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			handler, registration, capability := natCapabilityFixture(t, 36, 46, 58, 68)
			access := capabilityAccess(capability, registration)
			mutation.mutate(&access)

			_, err := handler.ConsumeAgentCompatNATCapability(access)

			require.True(t, errors.Is(err, ErrAgentCompatCapabilityHidden))
		})
	}
}

func TestAgentCompatCapabilityCancelBeforeBindWakesWaiterAndPreventsBind(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(37, 47), AgentCompatCapabilityTerminal, 59, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	access := capabilityAccess(capability, registration)
	started := make(chan struct{})
	var observed atomic.Bool
	handler.setAgentCompatCapabilityWaitObserverForTest(func() {
		if observed.CompareAndSwap(false, true) {
			close(started)
		}
	})
	result := make(chan error, 1)
	waitCtx := agentCompatCapabilityTestContext(t)
	go func() {
		_, waitErr := handler.WaitAgentCompatIOStreamCapability(waitCtx, access)
		result <- waitErr
	}()
	awaitAgentCompatCapabilitySignal(t, started, "wait observer did not start")

	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
	require.ErrorIs(t, receiveAgentCompatCapabilityError(t, result, "canceled waiter did not return"), ErrAgentCompatCapabilityHidden)
	require.NoError(t, handler.CreateStreamWithPurpose("after-cancel", 47, 59, PurposeTerminal))
	require.ErrorIs(t, handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: access, StreamID: "after-cancel"}), ErrAgentCompatCapabilityHidden)
}
