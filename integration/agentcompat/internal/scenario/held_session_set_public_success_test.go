//go:build linux

package scenario

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeldSessionSetPublicSuccessOrchestratesCanonicalLifecycle(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	result := make(chan *heldSessionSet, 1)
	errResult := make(chan error, 1)
	go func() {
		set, err := NewHeldSessionSet(context.Background(), fixture.input)
		result <- set
		errResult <- err
	}()
	for range fixture.plan.Sessions {
		<-fixture.coordinator.ready
	}
	permutation := []int{8, 1, 11, 3, 0, 10, 4, 7, 2, 9, 5, 6}
	fixture.coordinator.releaseAll()
	started := make([]string, 0, len(permutation))
	completed := make([]heldSessionConstructorCompletion, 0, len(permutation))
	acquired := make([]int, 0, len(permutation))
	for _, index := range permutation {
		fixture.coordinator.release(index)
		started = append(started, <-fixture.coordinator.startEvents)
		completion := <-fixture.coordinator.completed
		completed = append(completed, completion)
		require.Equal(t, index, completion.index)
		require.NoError(t, completion.err)
		require.True(t, completion.acquired)
		acquired = append(acquired, <-fixture.coordinator.acquired)
		require.Equal(t, index, acquired[len(acquired)-1])
	}
	set := <-result
	require.NoError(t, <-errResult)
	for range fixture.plan.Sessions {
		constructorContext := <-fixture.coordinator.contextSeen
		select {
		case <-constructorContext.Done():
			t.Fatal("constructor context was canceled after successful construction")
		default:
		}
	}
	counters := fixture.counters.values()
	require.Equal(t, 8, counters.inspect)
	require.Equal(t, 1, counters.snapshot)
	require.Equal(t, 1, counters.observe)
	require.Equal(t, 4, counters.terminal)
	require.Equal(t, 4, counters.nat)
	require.Equal(t, 4, counters.fm)
	require.Len(t, set.sessions, 12)
	for index, session := range set.sessions {
		require.Equal(t, fixture.plan.Sessions[index], session.Plan())
	}
	require.Equal(t, permutation, acquired)
	require.Equal(t, permutation, completedIndexes(completed))
	require.Equal(t, canonicalPlanIndexes(set), []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	require.Equal(t, permutationPlanIDs(fixture, permutation), started)
	require.NoError(t, set.Close(context.Background()))
	require.NoError(t, set.WaitHealthy(context.Background()))
	for index := 11; index >= 0; index-- {
		require.Equal(t, index, <-fixture.closeOrder)
		require.Equal(t, index, <-fixture.waitOrder)
	}
	require.Len(t, fixture.state.present, 12)
	require.Len(t, fixture.state.absent, 12)
	require.ElementsMatch(t, canonicalStreamIDs(fixture), fixture.state.present)
	require.ElementsMatch(t, canonicalStreamIDs(fixture), fixture.state.absent)
	require.Equal(t, 26, len(fixture.state.counts))
	require.Equal(t, 13, countExpectedState(fixture.state.counts, 19))
	require.Equal(t, 13, countExpectedState(fixture.state.counts, 7))
	require.Equal(t, 1, fixture.state.presentAggregateCalls)
	require.Equal(t, 1, fixture.state.absentAggregateCalls)
	require.True(t, fixture.state.aggregatePredicatesEmpty)
	set.healthWG.Wait()
}

func completedIndexes(completions []heldSessionConstructorCompletion) []int {
	indexes := make([]int, 0, len(completions))
	for _, completion := range completions {
		indexes = append(indexes, completion.index)
	}
	return indexes
}

func canonicalPlanIndexes(set *heldSessionSet) []int {
	indexes := make([]int, 0, len(set.sessions))
	for index := range set.sessions {
		indexes = append(indexes, index)
	}
	return indexes
}

func permutationPlanIDs(fixture *heldSessionSetPublicFixture, indexes []int) []string {
	ids := make([]string, 0, len(indexes))
	for _, index := range indexes {
		ids = append(ids, fixture.plan.Sessions[index].ID.String())
	}
	return ids
}

func canonicalStreamIDs(fixture *heldSessionSetPublicFixture) []string {
	ids := make([]string, 0, len(fixture.plan.Sessions))
	for index := range fixture.plan.Sessions {
		ids = append(ids, fixture.coordinator.sessions[index].streamID)
	}
	return ids
}

func countExpectedState(counts []int, expected int) int {
	count := 0
	for _, value := range counts {
		if value == expected {
			count++
		}
	}
	return count
}
