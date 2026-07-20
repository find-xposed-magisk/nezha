//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func TestHeldSessionSetErrorsRedactNestedIdentity(t *testing.T) {
	// Given
	secret := errors.New("stream=secret-stream uuid=secret-uuid server=991 authorization=secret-token")

	// When
	err := redactHeldSessionSetError(secret)

	// Then
	require.ErrorIs(t, err, secret)
	require.Equal(t, "held session set operation failed", err.Error())
	require.NotContains(t, err.Error(), "secret-stream")
	require.NotContains(t, err.Error(), "secret-token")
}

func TestHeldSessionSetStateChecksUseEveryExactID(t *testing.T) {
	// Given
	observer := &heldSessionSetStateFake{state: client.IOStreamState{Count: 12}}
	ids := []string{"stream-a", "stream-b", "stream-c"}

	// When
	err := waitHeldSessionSetStreams(context.Background(), observer, 12, ids, true, func(ctx context.Context, state heldSessionSetStateObserver, expectation client.IOStreamStateExpectation) (client.IOStreamState, error) {
		return state.(*heldSessionSetStateFake).wait(ctx, expectation)
	})

	// Then
	require.NoError(t, err)
	require.ElementsMatch(t, ids, observer.present)
}

func TestHeldSessionSetHealthUsesCanonicalIndexWhenClosuresRace(t *testing.T) {
	// Given
	firstError := errors.New("first canonical health failure")
	secondError := errors.New("second canonical health failure")
	firstPlan := StressSessionPlan{Kind: StressSessionTerminal, Ordinal: 1}
	secondPlan := StressSessionPlan{Kind: StressSessionNAT, Ordinal: 2}
	first := newHeldSessionHealthFake(t, firstPlan, firstError)
	second := newHeldSessionHealthFake(t, secondPlan, secondError)
	set := newHeldSessionSet([]StressSessionPlan{firstPlan, secondPlan}, &heldSessionSetStateFake{state: client.IOStreamState{Count: 2}}, client.IOStreamState{Count: 0}, HeldSessionSetDependencies{}, context.Background())
	set.sessions = []heldSession{first, second}
	set.startHealthWatchers()

	// When
	close(second.done)
	close(first.done)
	err := set.WaitHealthy(context.Background())
	set.stopHealthWatchers()
	set.healthWG.Wait()

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, firstError)
	require.NotErrorIs(t, err, secondError)
}
