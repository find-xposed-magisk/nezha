//go:build agentcompat

package rpc

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func capabilityOwner(patID, userID uint64) AgentCompatCapabilityOwner {
	return AgentCompatCapabilityOwner{PATID: patID, UserID: userID, IsAdmin: false}
}

func capabilityRegistration(owner AgentCompatCapabilityOwner, purpose AgentCompatCapabilityPurpose, serverID, resourceID uint64) AgentCompatCapabilityRegistration {
	return AgentCompatCapabilityRegistration{
		Owner: owner, Purpose: purpose, TargetServerID: serverID, ResourceID: resourceID, ServerAccessAllowed: true,
	}
}

func capabilityAccess(capability AgentCompatIOStreamCapability, registration AgentCompatCapabilityRegistration) AgentCompatCapabilityAccess {
	return AgentCompatCapabilityAccess{
		Capability: capability, Owner: registration.Owner, Purpose: registration.Purpose,
		TargetServerID: registration.TargetServerID, ResourceID: registration.ResourceID, ServerAccessAllowed: true,
	}
}

func TestAgentCompatCapabilityMintUsesURLSafe256BitTokens(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(1, 2), AgentCompatCapabilityTerminal, 3, 0)

	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)

	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(capability.String())
	require.NoError(t, err)
	require.Len(t, raw, 32)
	parsed, err := ParseAgentCompatIOStreamCapability(capability.String())
	require.NoError(t, err)
	require.Equal(t, capability, parsed)
	_, err = ParseAgentCompatIOStreamCapability("not-a-capability")
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
}

func TestAgentCompatCapabilityMintRetriesActiveAndUsedCollisions(t *testing.T) {
	handler := NewNezhaHandler()
	first := make([]byte, 32)
	second := make([]byte, 32)
	third := make([]byte, 32)
	first[0], second[0], third[0] = 1, 2, 3
	var calls atomic.Int32
	handler.setAgentCompatCapabilityTokenSourceForTest(func(destination []byte) error {
		switch calls.Add(1) {
		case 1, 2, 4:
			copy(destination, first)
			return nil
		case 3:
			copy(destination, second)
			return nil
		default:
			copy(destination, third)
			return nil
		}
	})
	registration := capabilityRegistration(capabilityOwner(1, 2), AgentCompatCapabilityTerminal, 3, 0)
	firstCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	activeCollisionCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	require.NotEqual(t, firstCapability, activeCollisionCapability)
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(firstCapability, registration)))

	tombstoneCollisionCapability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)

	require.NoError(t, err)
	require.NotEqual(t, firstCapability, tombstoneCollisionCapability)
	require.Equal(t, int32(5), calls.Load())
}

func TestAgentCompatCapabilityRegistrationRequiresServerAccessProof(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(1, 2), AgentCompatCapabilityTerminal, 3, 0)
	registration.ServerAccessAllowed = false

	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)

	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
}

func TestAgentCompatCapabilityWaitRequiresExactOwnerAndRetainsBinding(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(10, 20), AgentCompatCapabilityTerminal, 30, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	require.NoError(t, handler.CreateStreamWithPurpose("terminal-bound", 20, 30, PurposeTerminal))
	require.NoError(t, handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{
		AgentCompatCapabilityAccess: capabilityAccess(capability, registration), StreamID: "terminal-bound",
	}))

	streamID, err := handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), capabilityAccess(capability, registration))
	require.NoError(t, err)
	require.Equal(t, "terminal-bound", streamID)
	foreign := capabilityAccess(capability, registration)
	foreign.Owner.PATID++
	_, err = handler.WaitAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), foreign)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityHidden)
}

type capabilityCloseEndpoint struct {
	handler  *NezhaHandler
	streamID string
	err      error
	closed   atomic.Int32
}

func (endpoint *capabilityCloseEndpoint) Read([]byte) (int, error)       { return 0, io.EOF }
func (endpoint *capabilityCloseEndpoint) Write(data []byte) (int, error) { return len(data), nil }
func (endpoint *capabilityCloseEndpoint) Close() error {
	endpoint.closed.Add(1)
	endpoint.handler.StreamOwnership(endpoint.streamID)
	return endpoint.err
}

func TestAgentCompatCapabilityCancelClosesOutsideLockAndJoinsErrors(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityFileManager, "fm-close", 41)
	firstErr := errors.New("user close")
	secondErr := errors.New("agent close")
	first := &capabilityCloseEndpoint{handler: handler, streamID: "fm-close", err: firstErr}
	second := &capabilityCloseEndpoint{handler: handler, streamID: "fm-close", err: secondErr}
	require.NoError(t, handler.UserConnected("fm-close", first))
	require.NoError(t, handler.AgentConnected("fm-close", second))

	err := handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration))

	require.ErrorIs(t, err, firstErr)
	require.ErrorIs(t, err, secondErr)
	require.Equal(t, int32(1), first.closed.Load())
	require.Equal(t, int32(1), second.closed.Load())
}

func boundCapabilityFixture(t *testing.T, purpose AgentCompatCapabilityPurpose, streamID string, serverID uint64) (*NezhaHandler, AgentCompatCapabilityRegistration, AgentCompatIOStreamCapability) {
	t.Helper()
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(11, 21), purpose, serverID, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	require.NoError(t, handler.CreateStreamWithPurpose(streamID, 21, serverID, purpose.streamPurpose()))
	require.NoError(t, handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{
		AgentCompatCapabilityAccess: capabilityAccess(capability, registration), StreamID: streamID,
	}))
	return handler, registration, capability
}

func TestAgentCompatCapabilityCancelRacingCloseChangesGenerationOnce(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "race-close", 51)
	endpoint := &capabilityCloseEndpoint{handler: handler, streamID: "race-close"}
	require.NoError(t, handler.AgentConnected("race-close", endpoint))
	start := handler.SnapshotIOStreamState()
	ready := make(chan struct{})
	raceCtx := agentCompatCapabilityTestContext(t)
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		select {
		case <-ready:
			_ = handler.CloseStream("race-close")
		case <-raceCtx.Done():
		}
	}()
	go func() {
		defer waitGroup.Done()
		select {
		case <-ready:
			_ = handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration))
		case <-raceCtx.Done():
		}
	}()
	close(ready)
	raceDone := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(raceDone)
	}()
	awaitAgentCompatCapabilitySignal(t, raceDone, "cancel/close race did not complete")
	require.NoError(t, raceCtx.Err())

	state := handler.SnapshotIOStreamState()
	require.Equal(t, start.Generation+1, state.Generation)
	require.Equal(t, int32(1), endpoint.closed.Load())
}

func TestAgentCompatCapabilityWaitTimeoutKeepsRegistration(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(1, 2), AgentCompatCapabilityTerminal, 3, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = handler.WaitAgentCompatIOStreamCapability(ctx, capabilityAccess(capability, registration))
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))
}

func TestAgentCompatCapabilityWaitWakesAfterUnregister(t *testing.T) {
	handler := NewNezhaHandler()
	registration := capabilityRegistration(capabilityOwner(1, 2), AgentCompatCapabilityTerminal, 3, 0)
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	result := make(chan error, 1)
	started := make(chan struct{})
	handler.setAgentCompatCapabilityWaitObserverForTest(func() { close(started) })
	waitCtx := agentCompatCapabilityTestContext(t)
	go func() {
		_, waitErr := handler.WaitAgentCompatIOStreamCapability(waitCtx, capabilityAccess(capability, registration))
		result <- waitErr
	}()
	awaitAgentCompatCapabilitySignal(t, started, "wait observer did not start")
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))

	require.ErrorIs(t, receiveAgentCompatCapabilityError(t, result, "unregister did not wake waiter"), ErrAgentCompatCapabilityHidden)
}
