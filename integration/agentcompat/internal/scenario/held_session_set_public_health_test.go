//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeldSessionSetPublicPrematureHealthIsRetainedAndCanonical(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	firstError := sensitiveError("health-first")
	secondError := sensitiveError("health-second")
	fixture.coordinator.sessions[0].closeResult = firstError
	fixture.coordinator.sessions[1].closeResult = secondError
	indexOneClosureObserved := make(chan struct{})
	fixture.input.testHealthSnapshotRequestHook = func(index int) {
		if index == 0 {
			fixture.coordinator.sessions[0].prematureClose()
		}
	}
	fixture.input.testHealthSnapshotReplyHook = func(index int) {
		_ = index
	}
	fixture.input.testHealthClosureObservedHook = func(index int) {
		if index == 1 {
			close(indexOneClosureObserved)
		}
	}
	set := fixture.returnedSet(t)
	fixture.coordinator.sessions[1].prematureClose()
	<-indexOneClosureObserved
	fixture.input.testHealthSnapshotRequestHook = func(index int) {
		if index == 0 {
			fixture.coordinator.sessions[0].prematureClose()
		}
	}
	err := set.WaitHealthy(context.Background())
	require.ErrorIs(t, err, firstError)
	require.NotErrorIs(t, err, secondError)
	require.Equal(t, "held session set health failed", err.Error())
	require.NotContains(t, err.Error(), "secret-token")
	for index := 2; index < 12; index++ {
		fixture.coordinator.sessions[index].prematureClose()
	}
	require.ErrorIs(t, set.WaitHealthy(context.Background()), firstError)
	require.NoError(t, set.Close(context.Background()))
	require.ErrorIs(t, set.WaitHealthy(context.Background()), firstError)
	set.healthWG.Wait()
}

func TestHeldSessionSetPublicCanceledHealthWaiterRetainsLaterResult(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	waitContext, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, set.WaitHealthy(waitContext), context.Canceled)
	healthError := errors.New("health failure")
	fixture.coordinator.sessions[3].closeResult = healthError
	fixture.coordinator.sessions[3].prematureClose()
	require.ErrorIs(t, set.WaitHealthy(context.Background()), healthError)
	require.NoError(t, set.Close(context.Background()))
	set.healthWG.Wait()
}

func TestHeldSessionSetPublicOwnedCloseDoesNotCreateHealthFailure(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	require.NoError(t, set.Close(context.Background()))
	require.NoError(t, set.WaitHealthy(context.Background()))
	set.healthWG.Wait()
}
