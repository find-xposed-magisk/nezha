//go:build !agentcompat

package rpc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentCompatCapabilityDefaultBuildHasNoRegistryState(t *testing.T) {
	handler := NewNezhaHandler()
	state := reflect.ValueOf(handler.agentCompatCapabilities)

	require.Equal(t, 0, state.NumField())
}

func TestAgentCompatCapabilityDefaultBuildUsesStableUnavailableAndNoopContracts(t *testing.T) {
	handler := NewNezhaHandler()
	registration := AgentCompatCapabilityRegistration{}
	capability, err := handler.RegisterAgentCompatIOStreamCapability(context.Background(), registration)
	require.Empty(t, capability.String())
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	access := AgentCompatCapabilityAccess{}
	require.ErrorIs(t, handler.BindAgentCompatIOStreamCapability(AgentCompatCapabilityBinding{}), ErrAgentCompatCapabilityUnavailable)
	_, err = handler.ConsumeAgentCompatNATCapability(access)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	require.ErrorIs(t, handler.PublishAgentCompatNATStream(AgentCompatNATPublishHandle{}, AgentCompatNATPublication{}), ErrAgentCompatCapabilityUnavailable)
	_, err = handler.WaitAgentCompatIOStreamCapability(context.Background(), access)
	require.True(t, errors.Is(err, ErrAgentCompatCapabilityUnavailable))
	require.NoError(t, handler.CancelAgentCompatIOStreamCapability(access))
	require.NoError(t, handler.UnregisterAgentCompatIOStreamCapability(access))
}

func TestAgentCompatNATCapabilityForProfileDefaultBuildIsUnavailableAndNoOp(t *testing.T) {
	handler := NewNezhaHandler()
	access, handle, err := handler.ConsumeAgentCompatNATCapabilityForProfile("not-a-capability", 1, 2)

	require.Equal(t, AgentCompatCapabilityAccess{}, access)
	require.Equal(t, AgentCompatNATPublishHandle{}, handle)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
}

func TestAgentCompatNATAtomicStartDefaultBuildIsUnavailableAndStateless(t *testing.T) {
	handler := NewNezhaHandler()
	publicationOwned, err := handler.StartAgentCompatNATStream(AgentCompatNATPublishHandle{}, 0)

	require.False(t, publicationOwned)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	require.Equal(t, 0, handler.StreamCount())
}

func TestAgentCompatNATLeaseDefaultBuildIsUnavailableAndStateless(t *testing.T) {
	handler := NewNezhaHandler()
	lease, err := handler.CreateAgentCompatNATStream(AgentCompatNATPublishHandle{}, "known")

	require.Nil(t, lease)
	require.ErrorIs(t, err, ErrAgentCompatCapabilityUnavailable)
	require.NoError(t, handler.CloseAgentCompatNATStreamLease(nil))
	require.Equal(t, 0, handler.StreamCount())
}
