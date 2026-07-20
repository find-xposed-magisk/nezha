//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHeldSessionSetPublicConstructorFailureRollsBackEverySuccess(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	firstError := sensitiveError("constructor-first")
	secondError := sensitiveError("constructor-second")
	fixture.coordinator.errors[fixture.plan.Sessions[2].ID.String()] = firstError
	fixture.coordinator.errors[fixture.plan.Sessions[7].ID.String()] = secondError
	fixture.coordinator.errorReady[2] = make(chan struct{})
	fixture.coordinator.errorReady[7] = make(chan struct{})
	result := make(chan error, 1)
	go func() { _, err := NewHeldSessionSet(context.Background(), fixture.input); result <- err }()
	for range fixture.plan.Sessions {
		<-fixture.coordinator.ready
	}
	fixture.coordinator.releaseAll()
	for _, index := range []int{0, 1, 3, 4, 5, 6, 8, 9, 10, 11} {
		fixture.coordinator.release(index)
	}
	completed := make([]heldSessionConstructorCompletion, 0, len(fixture.plan.Sessions))
	acquired := make([]int, 0, 10)
	for range []int{0, 1, 3, 4, 5, 6, 8, 9, 10, 11} {
		completion := <-fixture.coordinator.completed
		completed = append(completed, completion)
		acquired = append(acquired, <-fixture.coordinator.acquired)
	}
	fixture.coordinator.release(2)
	fixture.coordinator.releaseError(2)
	firstCompletion := <-fixture.coordinator.completed
	completed = append(completed, firstCompletion)
	require.Equal(t, firstError, firstCompletion.err)
	fixture.coordinator.release(7)
	fixture.coordinator.releaseError(7)
	secondCompletion := <-fixture.coordinator.completed
	completed = append(completed, secondCompletion)
	require.Equal(t, secondError, secondCompletion.err)
	err := <-result
	require.Error(t, err)
	require.ErrorIs(t, err, firstError)
	require.ErrorIs(t, err, secondError)
	require.NotContains(t, err.Error(), "secret-token")
	require.NotContains(t, err.Error(), "secret-path")
	require.ElementsMatch(t, []int{0, 1, 3, 4, 5, 6, 8, 9, 10, 11}, acquired)
	wantRollback := []int{11, 10, 9, 8, 6, 5, 4, 3, 1, 0}
	for _, index := range wantRollback {
		require.Equal(t, index, <-fixture.closeOrder)
		require.Equal(t, index, <-fixture.waitOrder)
	}
	require.ElementsMatch(t, acquiredStreamIDs(fixture, acquired), fixture.state.absent)
}

func TestHeldSessionSetPublicConstructorFailureCancelsSiblingAcquisition(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	trigger := sensitiveError("constructor-trigger")
	blockedIndex := 7
	fixture.coordinator.blockedIndex = blockedIndex
	fixture.coordinator.blockedWaiting = make(chan struct{}, 1)
	fixture.coordinator.blockedCanceled = make(chan struct{}, 1)
	triggerIndex := 2
	fixture.coordinator.errors[fixture.plan.Sessions[triggerIndex].ID.String()] = trigger
	fixture.coordinator.errorReady[triggerIndex] = make(chan struct{})
	outerContext := context.Background()
	result := make(chan error, 1)
	go func() { _, err := NewHeldSessionSet(outerContext, fixture.input); result <- err }()
	for range fixture.plan.Sessions {
		<-fixture.coordinator.ready
	}
	fixture.coordinator.releaseAll()
	for _, index := range []int{0, 1, 3, 4, 5, 6, 8, 9, 10, 11} {
		fixture.coordinator.release(index)
	}
	for range []int{0, 1, 3, 4, 5, 6, 8, 9, 10, 11} {
		completion := <-fixture.coordinator.completed
		require.NoError(t, completion.err)
		<-fixture.coordinator.acquired
	}
	fixture.coordinator.release(blockedIndex)
	select {
	case <-fixture.coordinator.blockedWaiting:
	case <-time.After(time.Second):
		t.Fatal("blocked constructor did not start")
	}
	fixture.coordinator.release(triggerIndex)
	fixture.coordinator.releaseError(triggerIndex)
	triggerCompletion := <-fixture.coordinator.completed
	require.ErrorIs(t, triggerCompletion.err, trigger)
	select {
	case <-fixture.coordinator.blockedCanceled:
	case <-time.After(time.Second):
		t.Fatal("sibling constructor was not canceled")
	}
	require.ErrorIs(t, <-result, trigger)
	select {
	case <-outerContext.Done():
		t.Fatal("outer context was canceled")
	default:
	}
	for _, index := range []int{11, 10, 9, 8, 6, 5, 4, 3, 1, 0} {
		require.Equal(t, index, <-fixture.closeOrder)
		require.Equal(t, index, <-fixture.waitOrder)
	}
}

func TestHeldSessionSetPublicCanceledConstructionWaitsAndRollsBack(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	fixture.coordinator.lateSuccessIndex = 11
	fixture.coordinator.lateSuccessWaiting = make(chan struct{}, 1)
	fixture.coordinator.lateSuccessReady = make(chan struct{}, 1)
	fixture.coordinator.lateSuccessRelease = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := NewHeldSessionSet(ctx, fixture.input); result <- err }()
	for range fixture.plan.Sessions {
		<-fixture.coordinator.ready
	}
	for _, index := range []int{0, 4} {
		fixture.coordinator.release(index)
	}
	fixture.coordinator.releaseAll()
	for range []int{0, 4} {
		<-fixture.coordinator.acquired
	}
	<-fixture.coordinator.lateSuccessWaiting
	cancel()
	fixture.coordinator.release(11)
	<-fixture.coordinator.lateSuccessReady
	close(fixture.coordinator.lateSuccessRelease)
	err := <-result
	require.ErrorIs(t, err, context.Canceled)
	completions := make([]heldSessionConstructorCompletion, 0, len(fixture.plan.Sessions))
	for range fixture.plan.Sessions {
		completions = append(completions, <-fixture.coordinator.completed)
	}
	require.ElementsMatch(t, []int{0, 4, 11}, completionIndexes(completions, true))
	require.ElementsMatch(t, []int{1, 2, 3, 5, 6, 7, 8, 9, 10}, completionIndexes(completions, false))
	require.Equal(t, []int{11, 4, 0}, collectOrder(fixture.closeOrder, 3))
	require.Equal(t, []int{11, 4, 0}, collectOrder(fixture.waitOrder, 3))
	require.ElementsMatch(t, acquiredStreamIDs(fixture, []int{0, 4, 11}), fixture.state.absent)
}

func completionIndexes(completions []heldSessionConstructorCompletion, acquired bool) []int {
	indexes := make([]int, 0, len(completions))
	for _, completion := range completions {
		if completion.acquired == acquired {
			indexes = append(indexes, completion.index)
		}
	}
	return indexes
}

func acquiredStreamIDs(fixture *heldSessionSetPublicFixture, indexes []int) []string {
	ids := make([]string, 0, len(indexes))
	for _, index := range indexes {
		ids = append(ids, fixture.coordinator.sessions[index].streamID)
	}
	return ids
}

func collectOrder(events <-chan int, count int) []int {
	order := make([]int, 0, count)
	for index := 0; index < count; index++ {
		order = append(order, <-events)
	}
	return order
}

func TestHeldSessionSetPublicAllLiveFailuresCloseMembers(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*heldSessionSetPublicFixture)
		want   error
	}{
		{name: "wait live", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.coordinator.sessions[0].waitLiveError = sensitiveError("live")
		}, want: ErrHeldSessionSetOperation},
		{name: "wrong plan", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.coordinator.sessions[0].plan.Ordinal++ }, want: ErrHeldSessionSetOperation},
		{name: "empty stream", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.coordinator.sessions[0].streamID = "" }, want: ErrHeldSessionSetOperation},
		{name: "duplicate stream", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.coordinator.sessions[1].streamID = fixture.coordinator.sessions[0].streamID
		}, want: ErrHeldSessionSetOperation},
		{name: "present state", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.state.presentAggregateError = sensitiveError("present")
		}, want: ErrHeldSessionSetOperation},
		{name: "aggregate state", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.state.presentAggregateError = sensitiveError("aggregate")
		}, want: ErrHeldSessionSetOperation},
		{name: "closed before return", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.coordinator.sessions[0].prematureClose()
			fixture.coordinator.sessions[0].closeResult = sensitiveError("closed")
		}, want: ErrHeldSessionSetOperation},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newHeldSessionSetPublicFixture(t)
			testCase.mutate(fixture)
			result := make(chan error, 1)
			go func() { _, err := NewHeldSessionSet(context.Background(), fixture.input); result <- err }()
			for range fixture.plan.Sessions {
				<-fixture.coordinator.ready
			}
			fixture.releaseConstructors()
			err := <-result
			require.ErrorIs(t, err, testCase.want)
			require.NotContains(t, err.Error(), "secret-token")
		})
	}
}

func TestHeldSessionSetPublicTopologyFailureHasNoSideEffects(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	fixture.input.ControlClient = nil
	fixture.input.Dependencies = HeldSessionSetDependencies{Terminal: func(context.Context, heldTerminalInput) (heldSession, error) {
		return nil, errors.New("constructor called")
	}}
	_, err := NewHeldSessionSet(context.Background(), fixture.input)
	require.ErrorIs(t, err, ErrInvalidHeldSessionSetTopology)
	require.Empty(t, fixture.state.present)
	require.Empty(t, fixture.state.absent)
	require.Empty(t, fixture.coordinator.ready)
}
