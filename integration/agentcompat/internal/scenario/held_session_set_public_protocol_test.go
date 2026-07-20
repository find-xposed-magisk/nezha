//go:build linux

package scenario

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeldSessionSetPublicCoordinatorRejectsMalformedSnapshots(t *testing.T) {
	cases := []struct {
		name       string
		target     int
		mutate     func(heldHealthSnapshotRequest) heldHealthSnapshot
		validFirst bool
	}{
		{name: "stale epoch", target: 0, mutate: func(request heldHealthSnapshotRequest) heldHealthSnapshot {
			return heldHealthSnapshot{epoch: request.epoch - 1, index: 0}
		}},
		{name: "duplicate index", target: 1, mutate: func(request heldHealthSnapshotRequest) heldHealthSnapshot {
			return heldHealthSnapshot{epoch: request.epoch, index: 0}
		}},
		{name: "out of range index", target: 0, mutate: func(request heldHealthSnapshotRequest) heldHealthSnapshot {
			return heldHealthSnapshot{epoch: request.epoch, index: heldSessionSetHealthMemberCount}
		}},
		{name: "missing index substitution", target: 11, mutate: func(request heldHealthSnapshotRequest) heldHealthSnapshot {
			return heldHealthSnapshot{epoch: request.epoch, index: heldSessionSetHealthMemberCount - 2}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newHeldSessionSetPublicFixture(t)
			requests := make(chan int, heldSessionSetHealthMemberCount)
			overrideIndexes := make(chan int, 1)
			malformedSent := make(chan struct{})
			fixture.input.testHealthSnapshotRequestHook = func(index int) { requests <- index }
			fixture.input.testHealthSnapshotOverrideHook = func(index int, request heldHealthSnapshotRequest) *heldHealthSnapshot {
				if index != testCase.target {
					return nil
				}
				select {
				case <-malformedSent:
				default:
					close(malformedSent)
				}
				malformed := testCase.mutate(request)
				overrideIndexes <- malformed.index
				return &malformed
			}
			set := fixture.returnedSet(t)
			closeResult := make(chan error, 1)
			go func() { closeResult <- set.Close(context.Background()) }()
			err := set.WaitHealthy(context.Background())
			require.Error(t, err)
			require.Equal(t, "held session set health failed", err.Error())
			require.ErrorIs(t, err, ErrHeldSessionSetHealth)
			require.ErrorIs(t, err, ErrHeldSessionSetHealthProtocol)
			require.Equal(t, []int{testCase.mutate(heldHealthSnapshotRequest{epoch: 1}).index}, []int{<-overrideIndexes})
			require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, sortedHealthIndexes(requests))
			require.NoError(t, <-closeResult)
			<-set.coordinatorDone
			<-set.closeDone
			for _, watcherDone := range set.healthWatcherDone {
				<-watcherDone
			}
			for index := len(fixture.coordinator.sessions) - 1; index >= 0; index-- {
				require.Equal(t, index, <-fixture.closeOrder)
				require.Equal(t, index, <-fixture.waitOrder)
			}
		})
	}
}

func sortedHealthIndexes(events <-chan int) []int {
	indexes := collectHealthIndexes(events)
	sort.Ints(indexes)
	return indexes
}
