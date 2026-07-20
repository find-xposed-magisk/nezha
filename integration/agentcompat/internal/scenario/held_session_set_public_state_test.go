//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeldSessionSetPublicPresentStateRetainsEveryDistinctSentinel(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	expected := make(map[string]error, len(fixture.plan.Sessions))
	for index := range fixture.plan.Sessions {
		sentinel := errors.New("present-state-" + fixture.coordinator.sessions[index].streamID)
		expected[fixture.coordinator.sessions[index].streamID] = sentinel
		fixture.state.presentStreamErrors[fixture.coordinator.sessions[index].streamID] = sentinel
	}
	aggregateSentinel := errors.New("present-state-aggregate")
	fixture.state.presentAggregateError = aggregateSentinel
	result := make(chan error, 1)
	go func() {
		_, err := NewHeldSessionSet(context.Background(), fixture.input)
		result <- err
	}()
	for range fixture.plan.Sessions {
		<-fixture.coordinator.ready
	}
	fixture.releaseConstructors()
	err := <-result
	require.Error(t, err)
	for streamID, sentinel := range expected {
		require.ErrorIs(t, err, sentinel, streamID)
	}
	require.ErrorIs(t, err, aggregateSentinel)
	require.ElementsMatch(t, canonicalStreamIDs(fixture), fixture.state.present)
	require.Equal(t, 12, len(fixture.state.present))
	require.Equal(t, 26, fixture.counters.values().waitState)
	require.Equal(t, 1, fixture.state.presentAggregateCalls)
	require.True(t, fixture.state.aggregatePredicatesEmpty)
}

func TestHeldSessionSetPublicAbsentStateRetainsEveryDistinctSentinel(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	expected := make(map[string]error, len(fixture.plan.Sessions))
	for index := range fixture.plan.Sessions {
		sentinel := errors.New("absent-state-" + fixture.coordinator.sessions[index].streamID)
		expected[fixture.coordinator.sessions[index].streamID] = sentinel
		fixture.state.absentStreamErrors[fixture.coordinator.sessions[index].streamID] = sentinel
	}
	aggregateSentinel := errors.New("absent-state-aggregate")
	fixture.state.absentAggregateError = aggregateSentinel
	err := set.Close(context.Background())
	require.Error(t, err)
	for streamID, sentinel := range expected {
		require.ErrorIs(t, err, sentinel, streamID)
	}
	require.ErrorIs(t, err, aggregateSentinel)
	require.ElementsMatch(t, canonicalStreamIDs(fixture), fixture.state.absent)
	require.Equal(t, 26, fixture.counters.values().waitState)
	require.Equal(t, 1, fixture.state.absentAggregateCalls)
	require.True(t, fixture.state.aggregatePredicatesEmpty)
}
