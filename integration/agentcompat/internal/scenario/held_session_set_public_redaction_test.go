//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

var heldSessionSetSensitiveFragments = []string{"secret-token", "/secret/path", "secret-uuid", "secret-stream", "server=991", "authorization="}

func requireHeldSessionSetRedacted(t *testing.T, err error, class error, causes ...error) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, class.Error(), err.Error())
	require.ErrorIs(t, err, class)
	for _, cause := range causes {
		require.ErrorIs(t, err, cause)
	}
	for _, fragment := range heldSessionSetSensitiveFragments {
		require.NotContains(t, err.Error(), fragment, fragment)
	}
}

func TestHeldSessionSetPublicRedactionInitialSnapshot(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	sentinel := sensitiveBoundaryError("snapshot")
	fixture.state.snapshotError = sentinel
	_, err := NewHeldSessionSet(context.Background(), fixture.input)
	requireHeldSessionSetRedacted(t, err, ErrHeldSessionSetOperation, sentinel)
}

func TestHeldSessionSetPublicRedactionConstructorsByKind(t *testing.T) {
	kinds := []StressSessionKind{StressSessionTerminal, StressSessionNAT, StressSessionFM}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newHeldSessionSetPublicFixture(t)
			index := sessionIndexByKind(fixture, kind)
			sentinel := sensitiveBoundaryError(string(kind))
			fixture.coordinator.errors[fixture.plan.Sessions[index].ID.String()] = sentinel
			fixture.coordinator.errorReady[index] = make(chan struct{})
			result := make(chan error, 1)
			go func() {
				_, err := NewHeldSessionSet(context.Background(), fixture.input)
				result <- err
			}()
			for range fixture.plan.Sessions {
				<-fixture.coordinator.ready
			}
			fixture.coordinator.releaseAll()
			for member := range fixture.plan.Sessions {
				if member != index {
					fixture.coordinator.release(member)
				}
			}
			fixture.coordinator.release(index)
			fixture.coordinator.releaseError(index)
			for range fixture.plan.Sessions {
				<-fixture.coordinator.completed
			}
			requireHeldSessionSetRedacted(t, <-result, ErrHeldSessionSetOperation, sentinel)
		})
	}
}

func TestHeldSessionSetPublicRedactionWaitLiveAndStateBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		closeSet bool
		mutate   func(*heldSessionSetPublicFixture, error)
	}{
		{name: "wait live", mutate: func(fixture *heldSessionSetPublicFixture, sentinel error) {
			fixture.coordinator.sessions[0].waitLiveError = sentinel
		}},
		{name: "present per stream", mutate: func(fixture *heldSessionSetPublicFixture, sentinel error) {
			fixture.state.presentStreamErrors[fixture.coordinator.sessions[0].streamID] = sentinel
		}},
		{name: "present aggregate", mutate: func(fixture *heldSessionSetPublicFixture, sentinel error) {
			fixture.state.presentAggregateError = sentinel
		}},
		{name: "absent per stream", closeSet: true, mutate: func(fixture *heldSessionSetPublicFixture, sentinel error) {
			fixture.state.absentStreamErrors[fixture.coordinator.sessions[0].streamID] = sentinel
		}},
		{name: "absent aggregate", closeSet: true, mutate: func(fixture *heldSessionSetPublicFixture, sentinel error) {
			fixture.state.absentAggregateError = sentinel
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newHeldSessionSetPublicFixture(t)
			sentinel := sensitiveBoundaryError(testCase.name)
			testCase.mutate(fixture, sentinel)
			var err error
			if testCase.closeSet {
				set := fixture.returnedSet(t)
				err = set.Close(context.Background())
			} else {
				_, err = newHeldSessionSetWithReleasedConstructors(fixture)
			}
			requireHeldSessionSetRedacted(t, err, ErrHeldSessionSetOperation, sentinel)
		})
	}
}

func TestHeldSessionSetPublicRedactionHealthCloseAndAbsentBoundaries(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	closeSentinel := sensitiveBoundaryError("close")
	waitSentinel := sensitiveBoundaryError("wait-closed")
	absentSentinel := sensitiveBoundaryError("absent")
	fixture.coordinator.sessions[0].closeError = closeSentinel
	fixture.coordinator.sessions[1].waitClosedError = waitSentinel
	fixture.state.absentAggregateError = absentSentinel
	err := set.Close(context.Background())
	requireHeldSessionSetRedacted(t, err, ErrHeldSessionSetOperation, closeSentinel, waitSentinel, absentSentinel)
}

func TestHeldSessionSetPublicRedactionBaselineRestorationAggregate(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	sentinel := sensitiveBoundaryError("baseline-restoration")
	fixture.state.absentAggregateError = sentinel
	err := set.Close(context.Background())
	requireHeldSessionSetRedacted(t, err, ErrHeldSessionSetOperation, sentinel)
	require.Equal(t, fixture.state.expectedAbsentCount, fixture.state.baseline.Count)
	require.ElementsMatch(t, canonicalStreamIDs(fixture), fixture.state.absent)
}

func TestHeldSessionSetPublicRedactionPrematureHealthRetainsTypedErrors(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	sentinel := sensitiveBoundaryError("premature")
	fixture.coordinator.sessions[0].closeResult = sentinel
	fixture.coordinator.sessions[0].prematureClose()
	err := set.WaitHealthy(context.Background())
	requireHeldSessionSetRedacted(t, err, ErrHeldSessionSetHealth, sentinel)
	require.NoError(t, set.Close(context.Background()))
}

func TestHeldSessionSetPublicRedactionPrematureHealthNilCloseResult(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	fixture.coordinator.sessions[0].prematureClose()
	err := set.WaitHealthy(context.Background())
	requireHeldSessionSetRedacted(t, err, ErrHeldSessionSetHealth, ErrHeldSessionPrematureClose)
	require.NoError(t, set.Close(context.Background()))
}

func TestHeldSessionSetPublicRedactionProtocolHealth(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	fixture.input.testHealthSnapshotOverrideHook = func(index int, request heldHealthSnapshotRequest) *heldHealthSnapshot {
		if index != 0 {
			return nil
		}
		return &heldHealthSnapshot{epoch: request.epoch - 1, index: 0, event: &heldHealthEvent{err: sensitiveBoundaryError("protocol")}}
	}
	set := fixture.returnedSet(t)
	fixture.coordinator.sessions[11].prematureClose()
	err := set.WaitHealthy(context.Background())
	requireHeldSessionSetRedacted(t, err, ErrHeldSessionSetHealth, ErrHeldSessionSetHealthProtocol)
	require.NoError(t, set.Close(context.Background()))
}

func sensitiveBoundaryError(label string) error {
	return errors.New(label + " token=secret-token path=/secret/path uuid=secret-uuid stream=secret-stream server=991 authorization=secret-token")
}

func sessionIndexByKind(fixture *heldSessionSetPublicFixture, kind StressSessionKind) int {
	for index, session := range fixture.plan.Sessions {
		if session.Kind == kind {
			return index
		}
	}
	panic("session kind not found")
}

func newHeldSessionSetWithReleasedConstructors(fixture *heldSessionSetPublicFixture) (*heldSessionSet, error) {
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
	fixture.releaseConstructors()
	return <-result, <-errResult
}
