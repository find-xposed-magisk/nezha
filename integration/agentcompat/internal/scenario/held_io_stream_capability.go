//go:build linux

package scenario

import (
	"context"
	"errors"
	"sync"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

type heldIOStreamCapabilityIdentity struct {
	Purpose    client.IOStreamCapabilityPurpose
	ServerID   uint64
	ResourceID uint64
}

type heldIOStreamCapability struct {
	client   client.IOStreamCapabilityClient
	identity heldIOStreamCapabilityIdentity
	access   client.IOStreamCapabilityAccessRequest
	streamID string

	mu             sync.Mutex
	waitOnce       sync.Once
	waitErr        error
	cancelOnce     sync.Once
	unregisterOnce sync.Once
	cancelErr      error
	unregisterErr  error
}

func registerHeldIOStreamCapability(ctx context.Context, transport *client.Client, identity heldIOStreamCapabilityIdentity) (*heldIOStreamCapability, error) {
	registered, err := transport.IOStreamCapabilities().Register(ctx, client.IOStreamCapabilityRegisterRequest{
		Purpose: identity.Purpose, ServerID: identity.ServerID, ResourceID: identity.ResourceID,
	})
	if err != nil {
		return nil, err
	}
	return &heldIOStreamCapability{
		client: transport.IOStreamCapabilities(), identity: identity,
		access: client.IOStreamCapabilityAccessRequest{Capability: registered.Capability, Purpose: identity.Purpose, ServerID: identity.ServerID, ResourceID: identity.ResourceID},
	}, nil
}

func (capability *heldIOStreamCapability) HeaderCapability() client.IOStreamCapability {
	return capability.access.Capability
}

func (capability *heldIOStreamCapability) Wait(ctx context.Context) (string, error) {
	capability.waitOnce.Do(func() {
		response, err := capability.client.Wait(ctx, client.IOStreamCapabilityWaitRequest(capability.access))
		if err != nil {
			capability.waitErr = err
			return
		}
		capability.streamID = response.StreamID.Value()
		if capability.streamID == "" {
			capability.waitErr = client.ErrIOStreamCapabilityUnavailable
		}
	})
	capability.mu.Lock()
	defer capability.mu.Unlock()
	if capability.waitErr != nil {
		return "", capability.waitErr
	}
	streamID := capability.streamID
	return streamID, nil
}

func (capability *heldIOStreamCapability) Cancel(ctx context.Context) error {
	capability.cancelOnce.Do(func() {
		capability.mu.Lock()
		defer capability.mu.Unlock()
		capability.cancelErr = capability.client.Cancel(ctx, capability.access)
	})
	capability.mu.Lock()
	defer capability.mu.Unlock()
	return capability.cancelErr
}

func (capability *heldIOStreamCapability) Unregister(ctx context.Context) error {
	capability.unregisterOnce.Do(func() {
		capability.mu.Lock()
		defer capability.mu.Unlock()
		capability.unregisterErr = capability.client.Unregister(ctx, capability.access)
	})
	capability.mu.Lock()
	defer capability.mu.Unlock()
	return capability.unregisterErr
}

func (capability *heldIOStreamCapability) streamIDValue() string {
	capability.mu.Lock()
	defer capability.mu.Unlock()
	return capability.streamID
}

func (capability *heldIOStreamCapability) waitExpectation(ctx context.Context, stateClient *client.Client, _ client.IOStreamState, absent bool) error {
	streamID := capability.streamIDValue()
	// Adapter-local ownership must not couple to the shared global count during concurrent construction.
	expectation := client.IOStreamStateExpectation{}
	if absent {
		if streamID != "" {
			expectation.AbsentStreamID = streamID
		} else {
			if _, waitErr := capability.Wait(ctx); waitErr == nil {
				streamID = capability.streamIDValue()
				expectation.AbsentStreamID = streamID
			} else if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	} else {
		if streamID == "" {
			return errors.New("held capability stream ID is missing")
		}
		expectation.PresentStreamID = streamID
	}
	_, err := stateClient.WaitForIOStreamState(ctx, expectation)
	return err
}

func (capability *heldIOStreamCapability) WaitExpectation(ctx context.Context, stateClient *client.Client, baseline client.IOStreamState, absent bool) error {
	return capability.waitExpectation(ctx, stateClient, baseline, absent)
}
