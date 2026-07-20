//go:build linux

package dashboard

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func TestDashboardIOStreamStateEndpointUsesPATAndRedactsStreamIdentity(t *testing.T) {
	// Given
	dashboard := startDashboard(t, false)
	authenticated := dashboard.Clients().MCP
	anonymous, err := client.New(client.Config{BaseURL: dashboard.URL()})
	require.NoError(t, err)

	// When
	state, err := authenticated.IOStreamState(t.Context())

	// Then
	require.NoError(t, err)
	require.Equal(t, 0, state.Count)
	require.Zero(t, state.Generation)

	// When
	satisfied, err := authenticated.WaitForIOStreamState(t.Context(), client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(0)})

	// Then
	require.NoError(t, err)
	require.Equal(t, state, satisfied)

	absent, err := authenticated.WaitForIOStreamState(t.Context(), client.IOStreamStateExpectation{AbsentStreamID: "absence-only"})
	require.NoError(t, err)
	require.Equal(t, state, absent)

	_, err = authenticated.WaitForIOStreamState(t.Context(), client.IOStreamStateExpectation{})
	require.ErrorIs(t, err, client.ErrSemanticFailure)

	// When
	_, err = authenticated.WaitForIOStreamState(t.Context(), client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(-1), AbsentStreamID: "private-stream-id"})

	// Then
	require.ErrorIs(t, err, client.ErrSemanticFailure)
	require.NotContains(t, err.Error(), "private-stream-id")

	// When
	anonymousState, anonymousErr := anonymous.IOStreamState(t.Context())
	anonymousWait, anonymousWaitErr := anonymous.WaitForIOStreamState(t.Context(), client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(0)})

	// Then
	require.Error(t, anonymousErr)
	require.ErrorIs(t, anonymousErr, client.ErrUnauthorized)
	require.Error(t, anonymousWaitErr)
	require.ErrorIs(t, anonymousWaitErr, client.ErrUnauthorized)
	require.Zero(t, anonymousState)
	require.Zero(t, anonymousWait)
}
