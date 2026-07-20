//go:build agentcompat

package rpc

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatCapabilityConcurrentRegistrationEnforcesExactPerPATQuota(t *testing.T) {
	handler := NewNezhaHandler()
	setUniqueAgentCompatCapabilityTokens(handler)
	registration := capabilityRegistration(capabilityOwner(104, 204), AgentCompatCapabilityTerminal, 305, 0)
	start := make(chan struct{})
	results := make(chan error, 64)
	var waitGroup sync.WaitGroup
	waitGroup.Add(64)
	for range 64 {
		go func() {
			defer waitGroup.Done()
			<-start
			_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
			results <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	succeeded, unavailable := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrAgentCompatCapabilityUnavailable):
			unavailable++
		default:
			t.Fatalf("unexpected registration error: %v", err)
		}
	}
	require.Equal(t, 16, succeeded)
	require.Equal(t, 48, unavailable)
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 16, active)
	require.Equal(t, 16, used)
}

func TestAgentCompatCapabilityConcurrentRegistrationEnforcesExactGlobalQuota(t *testing.T) {
	handler := NewNezhaHandler()
	setUniqueAgentCompatCapabilityTokens(handler)
	start := make(chan struct{})
	results := make(chan error, 256)
	var waitGroup sync.WaitGroup
	waitGroup.Add(256)
	for index := range 256 {
		go func() {
			defer waitGroup.Done()
			<-start
			registration := capabilityRegistration(capabilityOwner(uint64(index+1), uint64(index+1001)), AgentCompatCapabilityTerminal, 306, 0)
			_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
			results <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	succeeded, unavailable := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
		unavailable++
	}
	require.Equal(t, 128, succeeded)
	require.Equal(t, 128, unavailable)
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 128, active)
	require.Equal(t, 128, used)
}

func TestAgentCompatCapabilityConcurrentRegistrationEnforcesExactProcessMintQuota(t *testing.T) {
	handler := NewNezhaHandler()
	setUniqueAgentCompatCapabilityTokens(handler)
	handler.ioStreamMutex.Lock()
	for index := range agentCompatCapabilityMaxProcessMints - 1 {
		handler.agentCompatCapabilities.used[string(rune(index+1))] = struct{}{}
	}
	handler.ioStreamMutex.Unlock()
	start := make(chan struct{})
	results := make(chan error, 2)
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	for index := range 2 {
		go func() {
			defer waitGroup.Done()
			<-start
			registration := capabilityRegistration(capabilityOwner(uint64(index+201), uint64(index+301)), AgentCompatCapabilityTerminal, 312, 0)
			_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
			results <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	succeeded, unavailable := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
		unavailable++
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, unavailable)
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 1, active)
	require.Equal(t, agentCompatCapabilityMaxProcessMints, used)
}

func TestAgentCompatCapabilityRemovalRequiresExactActiveRegistration(t *testing.T) {
	handler := NewNezhaHandler()
	setUniqueAgentCompatCapabilityTokens(handler)
	registration := capabilityRegistration(capabilityOwner(105, 205), AgentCompatCapabilityTerminal, 307, 0)
	capability := registerAgentCompatCapability(t, handler, registration)
	handler.ioStreamMutex.Lock()
	activeRegistration := handler.agentCompatCapabilities.active[capability.value]
	staleRegistration := &agentCompatCapabilityRegistration{registration: registration, notify: make(chan struct{})}
	handler.removeAgentCompatCapabilityLocked(capability.value, staleRegistration)
	handler.ioStreamMutex.Unlock()
	for range 15 {
		registerAgentCompatCapability(t, handler, registration)
	}

	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 16, active)
	require.Equal(t, 16, used)

	handler.ioStreamMutex.Lock()
	handler.removeAgentCompatCapabilityLocked(capability.value, activeRegistration)
	handler.removeAgentCompatCapabilityLocked(capability.value, activeRegistration)
	handler.ioStreamMutex.Unlock()

	replacement := registerAgentCompatCapability(t, handler, registration)
	require.NotEmpty(t, replacement.String())
	active, used = agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 16, active)
	require.Equal(t, 17, used)
}

func TestAgentCompatCapabilityForeignRemovalDoesNotReleasePerPATQuota(t *testing.T) {
	handler := NewNezhaHandler()
	setUniqueAgentCompatCapabilityTokens(handler)
	registration := capabilityRegistration(capabilityOwner(106, 206), AgentCompatCapabilityTerminal, 308, 0)
	capability := registerAgentCompatCapability(t, handler, registration)
	for range 15 {
		registerAgentCompatCapability(t, handler, registration)
	}
	foreign := capabilityAccess(capability, registration)
	foreign.Owner.PATID++

	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(foreign))
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(foreign))
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(AgentCompatCapabilityAccess{}))
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(AgentCompatCapabilityAccess{}))
	_, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	active, used := agentCompatCapabilityRegistryCounts(handler)
	require.Equal(t, 16, active)
	require.Equal(t, 16, used)
}

func TestAgentCompatCapabilityCancelReleasesQuotaBeforeEndpointCloseFailure(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "quota-close-failure", 309)
	setUniqueAgentCompatCapabilityTokens(handler)
	closeErr := errors.New("endpoint close failed")
	endpoint := &capabilityCloseEndpoint{handler: handler, streamID: "quota-close-failure", err: closeErr}
	require.NoError(t, handler.AgentConnected("quota-close-failure", endpoint))
	for range 15 {
		registerAgentCompatCapability(t, handler, registration)
	}

	err := handler.CancelAgentCompatIOStreamCapability(capabilityAccess(capability, registration))

	require.ErrorIs(t, err, closeErr)
	replacement := registerAgentCompatCapability(t, handler, registration)
	require.NotEmpty(t, replacement.String())
	activeForPAT, exists := agentCompatCapabilityActiveForPAT(handler, registration.Owner.PATID)
	require.True(t, exists)
	require.Equal(t, uint16(16), activeForPAT)
}

func TestAgentCompatCapabilityBoundUnregisterConflictRetainsQuota(t *testing.T) {
	handler, registration, capability := boundCapabilityFixture(t, AgentCompatCapabilityTerminal, "quota-bound-conflict", 310)
	setUniqueAgentCompatCapabilityTokens(handler)
	for range 15 {
		registerAgentCompatCapability(t, handler, registration)
	}

	err := handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration))
	_, registerErr := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)

	require.ErrorIs(t, err, ErrAgentCompatCapabilityBound)
	require.ErrorIs(t, registerErr, ErrAgentCompatCapabilityUnavailable)
	activeForPAT, exists := agentCompatCapabilityActiveForPAT(handler, registration.Owner.PATID)
	require.True(t, exists)
	require.Equal(t, uint16(16), activeForPAT)
}

func TestAgentCompatCapabilityLastRemovalDeletesPerPATAccountingEntry(t *testing.T) {
	handler := NewNezhaHandler()
	setUniqueAgentCompatCapabilityTokens(handler)
	registration := capabilityRegistration(capabilityOwner(107, 207), AgentCompatCapabilityTerminal, 311, 0)
	capability := registerAgentCompatCapability(t, handler, registration)

	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(capabilityAccess(capability, registration)))

	activeForPAT, exists := agentCompatCapabilityActiveForPAT(handler, registration.Owner.PATID)
	require.False(t, exists)
	require.Zero(t, activeForPAT)
}
