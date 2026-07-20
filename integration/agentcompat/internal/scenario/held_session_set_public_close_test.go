//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeldSessionSetPublicCloseJoinsReverseCleanupAndStateErrors(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	closeError := sensitiveError("close")
	waitClosedError := sensitiveError("wait-closed")
	stateError := sensitiveError("absent")
	for index, session := range fixture.coordinator.sessions {
		if index%3 == 0 {
			session.closeError = closeError
		}
		if index%4 == 0 {
			session.waitClosedError = waitClosedError
		}
	}
	fixture.state.absentAggregateError = stateError
	err := set.Close(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, closeError)
	require.ErrorIs(t, err, waitClosedError)
	require.ErrorIs(t, err, stateError)
	require.Equal(t, "held session set operation failed", err.Error())
	require.NotContains(t, err.Error(), "secret-token")
	for index := 11; index >= 0; index-- {
		require.Equal(t, index, <-fixture.closeOrder)
		require.Equal(t, index, <-fixture.waitOrder)
	}
	require.Len(t, fixture.state.absent, 12)
}

func TestHeldSessionSetPublicConcurrentAndCanceledCloseShareOwnerResult(t *testing.T) {
	fixture := newHeldSessionSetPublicFixture(t)
	set := fixture.returnedSet(t)
	closeError := errors.New("owner close error")
	fixture.coordinator.sessions[0].closeError = closeError
	closeStarted := make(chan struct{})
	closeRelease := make(chan struct{})
	fixture.coordinator.sessions[11].closeStarted = closeStarted
	fixture.coordinator.sessions[11].closeRelease = closeRelease
	ownerResult := make(chan error, 1)
	go func() { ownerResult <- set.Close(context.Background()) }()
	<-closeStarted
	canceled, cancel := context.WithCancel(context.Background())
	waiterResult := make(chan error, 1)
	go func() { waiterResult <- set.Close(canceled) }()
	cancel()
	require.ErrorIs(t, <-waiterResult, context.Canceled)
	close(closeRelease)
	require.ErrorIs(t, <-ownerResult, closeError)
	thirdResult := set.Close(context.Background())
	require.ErrorIs(t, thirdResult, closeError)
	require.Equal(t, "held session set operation failed", thirdResult.Error())
	for index := 11; index >= 0; index-- {
		require.Equal(t, index, <-fixture.closeOrder)
		require.Equal(t, index, <-fixture.waitOrder)
	}
}
