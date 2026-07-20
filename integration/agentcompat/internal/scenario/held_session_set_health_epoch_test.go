//go:build linux

package scenario

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func collectHealthIndexes(events <-chan int) []int {
	indexes := make([]int, 0, heldSessionSetHealthMemberCount)
	for index := 0; index < heldSessionSetHealthMemberCount; index++ {
		indexes = append(indexes, <-events)
	}
	return indexes
}

func TestHeldSessionSetHealthActiveEpochCompletesEveryRequestBeforeShutdown(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	requestIndexes := make(chan int, heldSessionSetHealthMemberCount)
	replyIndexes := make(chan int, heldSessionSetHealthMemberCount)
	releaseBroadcast := make(chan struct{})
	broadcastReached := make(chan struct{})
	fixture.input.testHealthSnapshotRequestHook = func(index int) {
		requestIndexes <- index
		if index == 5 {
			close(broadcastReached)
			<-releaseBroadcast
		}
	}
	fixture.input.testHealthSnapshotReplyHook = func(index int) { replyIndexes <- index }
	set := fixture.returnedSet(t)
	fixture.coordinator.sessions[11].closeResult = errors.New("active epoch trigger")
	closeStarted := make(chan struct{})
	fixture.coordinator.sessions[11].closeStarted = closeStarted
	fixture.coordinator.sessions[11].prematureClose()
	<-broadcastReached
	closeResult := make(chan error, 1)
	go func() { closeResult <- set.Close(context.Background()) }()
	close(releaseBroadcast)
	require.NoError(t, <-closeResult)
	<-closeStarted
	select {
	case <-set.coordinatorDone:
	default:
		t.Fatal("member Close started before coordinator completion")
	}
	requests := collectHealthIndexes(requestIndexes)
	replies := collectHealthIndexes(replyIndexes)
	sort.Ints(requests)
	sort.Ints(replies)
	require.Equal(t, requests, replies)
	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, requests)
	require.Error(t, set.WaitHealthy(context.Background()))
}

func TestHeldSessionSetHealthFinalCutRetainsAcceptedSnapshotAgainstLaterClosure(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	highEventSent := make(chan struct{})
	shutdownAccepted := make(chan struct{})
	shutdownAcknowledged := make(chan struct{})
	indexZeroSendReady := make(chan struct{})
	indexZeroSendAllowed := make(chan struct{})
	indexZeroAccepted := make(chan struct{})
	indexZeroClosureObserved := make(chan struct{})
	releaseOtherSnapshots := make(chan struct{})
	lateClosure := errors.New("late closure")
	fixture.input.testHealthEventHook = func(message heldHealthMessage) {
		if message.event != nil && message.event.index == 11 {
			close(highEventSent)
		}
	}
	fixture.input.testHealthShutdownAcceptedHook = func() { close(shutdownAccepted) }
	fixture.input.testHealthShutdownAcknowledgedHook = func() { close(shutdownAcknowledged) }
	fixture.input.testHealthSnapshotAcceptedHook = func(snapshot heldHealthSnapshot) {
		if snapshot.index == 0 {
			close(indexZeroAccepted)
		}
	}
	fixture.input.testHealthSnapshotSendHook = func(index int) {
		if index == 0 {
			close(indexZeroSendReady)
			<-indexZeroSendAllowed
		} else {
			<-releaseOtherSnapshots
		}
	}
	fixture.input.testHealthClosureObservedHook = func(index int) {
		if index == 0 {
			close(indexZeroClosureObserved)
		}
	}
	set := fixture.returnedSet(t)
	triggerError := errors.New("trigger")
	fixture.coordinator.sessions[11].closeResult = triggerError
	fixture.coordinator.sessions[11].prematureClose()
	<-highEventSent
	closeResult := make(chan error, 1)
	go func() { closeResult <- set.Close(context.Background()) }()
	<-indexZeroSendReady
	<-shutdownAccepted
	close(indexZeroSendAllowed)
	<-indexZeroAccepted
	select {
	case <-fixture.closeOrder:
		t.Fatal("member cleanup started before remaining snapshots were released")
	default:
	}
	fixture.coordinator.sessions[0].closeResult = lateClosure
	fixture.coordinator.sessions[0].prematureClose()
	<-indexZeroClosureObserved
	select {
	case <-fixture.closeOrder:
		t.Fatal("member cleanup started before blocked snapshots were released")
	default:
	}
	close(releaseOtherSnapshots)
	require.NoError(t, <-closeResult)
	require.ErrorIs(t, set.WaitHealthy(context.Background()), triggerError)
	require.NotErrorIs(t, set.WaitHealthy(context.Background()), lateClosure)
	<-shutdownAcknowledged
	<-set.coordinatorDone
	for _, watcherDone := range set.healthWatcherDone {
		<-watcherDone
	}
	<-set.closeDone
	for index := 11; index >= 0; index-- {
		require.Equal(t, index, <-fixture.closeOrder)
		require.Equal(t, index, <-fixture.waitOrder)
	}
}

func TestHeldSessionSetHealthEpochRejectsDuplicateReply(t *testing.T) {
	protocol := newHeldHealthEpochProtocol(1)
	protocol.accept(heldHealthSnapshot{epoch: 1, index: 0})
	protocol.accept(heldHealthSnapshot{epoch: 1, index: 0})
	for index := 1; index < heldSessionSetHealthMemberCount; index++ {
		protocol.accept(heldHealthSnapshot{epoch: 1, index: index})
	}
	require.ErrorIs(t, protocol.err, ErrHeldSessionSetHealthProtocol)
	require.False(t, protocol.committed)
}

func TestHeldSessionSetHealthEpochRejectsOutOfRangeReply(t *testing.T) {
	protocol := newHeldHealthEpochProtocol(1)
	protocol.accept(heldHealthSnapshot{epoch: 1, index: heldSessionSetHealthMemberCount})
	require.ErrorIs(t, protocol.err, ErrHeldSessionSetHealthProtocol)
	require.False(t, protocol.committed)
}

func TestHeldSessionSetHealthEpochRejectsMissingIndexSubstitution(t *testing.T) {
	protocol := newHeldHealthEpochProtocol(1)
	for index := 0; index < heldSessionSetHealthMemberCount-1; index++ {
		protocol.accept(heldHealthSnapshot{epoch: 1, index: index})
	}
	protocol.accept(heldHealthSnapshot{epoch: 1, index: heldSessionSetHealthMemberCount - 2})
	require.ErrorIs(t, protocol.err, ErrHeldSessionSetHealthProtocol)
	require.False(t, protocol.committed)
}

func TestHeldSessionSetHealthProtocolErrorAcknowledgesShutdownAndFinishes(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	requestReached := make(chan struct{})
	releaseRequest := make(chan struct{})
	fixture.input.testHealthSnapshotRequestHook = func(index int) {
		if index != 4 {
			return
		}
		close(requestReached)
		<-releaseRequest
	}
	set := fixture.returnedSet(t)
	fixture.coordinator.sessions[11].closeResult = sensitiveError("protocol-trigger")
	fixture.coordinator.sessions[11].prematureClose()
	<-requestReached
	closeResult := make(chan error, 1)
	go func() { closeResult <- set.Close(context.Background()) }()
	set.healthEvents <- heldHealthMessage{snapshot: &heldHealthSnapshot{epoch: 1, index: heldSessionSetHealthMemberCount}}
	close(releaseRequest)
	require.NoError(t, <-closeResult)
	require.ErrorIs(t, set.WaitHealthy(context.Background()), ErrHeldSessionSetHealthProtocol)
	require.ErrorIs(t, set.WaitHealthy(context.Background()), ErrHeldSessionSetHealth)
	require.Equal(t, "held session set health failed", set.WaitHealthy(context.Background()).Error())
	<-set.coordinatorDone
	set.healthWG.Wait()
}

func TestHeldSessionSetHealthEpochIncludesCloseBeforeOpenReply(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	fixture.input.testHealthSnapshotRequestHook = func(index int) {
		if index == 0 {
			fixture.coordinator.sessions[0].prematureClose()
		}
	}
	fixture.input.testHealthSnapshotReplyHook = func(int) {}
	set := fixture.returnedSet(t)
	highError := sensitiveError("epoch-high")
	lowError := sensitiveError("epoch-low")
	fixture.coordinator.sessions[11].closeResult = highError
	fixture.coordinator.sessions[0].closeResult = lowError
	fixture.coordinator.sessions[11].prematureClose()
	err := set.WaitHealthy(context.Background())
	require.ErrorIs(t, err, lowError)
	require.NotErrorIs(t, err, highError)
	require.Equal(t, "held session set health failed", err.Error())
	require.NotContains(t, err.Error(), "secret-token")
	require.NoError(t, set.Close(context.Background()))
}

func TestHeldSessionSetHealthEpochExcludesCloseAfterOpenReply(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	fixture.input.testHealthSnapshotRequestHook = func(int) {}
	fixture.input.testHealthSnapshotReplyHook = func(index int) {
		if index == 0 {
			fixture.coordinator.sessions[0].prematureClose()
		}
	}
	set := fixture.returnedSet(t)
	highError := sensitiveError("epoch-high")
	lowError := sensitiveError("epoch-low")
	fixture.coordinator.sessions[11].closeResult = highError
	fixture.coordinator.sessions[0].closeResult = lowError
	fixture.coordinator.sessions[11].prematureClose()
	err := set.WaitHealthy(context.Background())
	require.ErrorIs(t, err, highError)
	require.NotErrorIs(t, err, lowError)
	require.Equal(t, "held session set health failed", err.Error())
	require.NoError(t, set.Close(context.Background()))
}

func TestHeldSessionSetHealthEpochCommitsWithoutUnrelatedDone(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	triggerError := sensitiveError("epoch-trigger")
	fixture.coordinator.sessions[11].closeResult = triggerError
	fixture.coordinator.sessions[11].prematureClose()
	err := set.WaitHealthy(context.Background())
	require.ErrorIs(t, err, triggerError)
	require.Equal(t, "held session set health failed", err.Error())
	require.NoError(t, set.Close(context.Background()))
	require.ErrorIs(t, set.WaitHealthy(context.Background()), triggerError)
}
