//go:build linux

package dashboard

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newWaiterDashboard() *Dashboard {
	return &Dashboard{
		receiptEvents: make(chan string),
		eventNotify:   make(chan struct{}),
		info2Events:   make(map[string]struct{}),
		stateEvents:   make(map[stateEventIdentity]struct{}),
	}
}

func (dashboard *Dashboard) publishTestEvent() {
	dashboard.eventMu.Lock()
	close(dashboard.eventNotify)
	dashboard.eventNotify = make(chan struct{})
	dashboard.eventMu.Unlock()
}

func TestDashboardWaiters_AllObserveCachedEvents(t *testing.T) {
	// Given
	dashboard := newWaiterDashboard()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	waiters := []func(context.Context) error{
		func(ctx context.Context) error { return dashboard.WaitForInfo2(ctx, 7, "uuid") },
		func(ctx context.Context) error { return dashboard.WaitForInfo2(ctx, 7, "uuid") },
		func(ctx context.Context) error { return dashboard.WaitForState(ctx, 2) },
		func(ctx context.Context) error { return dashboard.WaitForState(ctx, 2) },
		func(ctx context.Context) error { return dashboard.WaitForReceiptAccepted(ctx) },
		func(ctx context.Context) error { return dashboard.WaitForReceiptAccepted(ctx) },
	}
	results := make(chan error, len(waiters))
	var group sync.WaitGroup
	group.Add(len(waiters))
	for _, wait := range waiters {
		go func(wait func(context.Context) error) {
			defer group.Done()
			results <- wait(ctx)
		}(wait)
	}
	// When
	dashboard.info2Mu.Lock()
	dashboard.info2Events["info2 7 uuid\n"] = struct{}{}
	dashboard.info2Mu.Unlock()
	dashboard.stateMu.Lock()
	dashboard.stateEvents[stateEventIdentity{ServerID: 7, UUID: "uuid", Generation: 1, Count: 2}] = struct{}{}
	dashboard.stateMu.Unlock()
	dashboard.receiptMu.Lock()
	dashboard.receiptAcceptedCount = 1
	dashboard.receiptMu.Unlock()
	dashboard.publishTestEvent()
	group.Wait()

	// Then
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.NoError(t, dashboard.WaitForInfo2(ctx, 7, "uuid"))
	require.NoError(t, dashboard.WaitForState(ctx, 2))
	require.NoError(t, dashboard.WaitForReceiptAccepted(ctx))
}

func TestDashboardWaiters_CloseWakesAllWithTypedError(t *testing.T) {
	// Given
	dashboard := newWaiterDashboard()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	results := make(chan error, 4)
	go func() { results <- dashboard.WaitForInfo2(ctx, 7, "uuid") }()
	go func() { results <- dashboard.WaitForState(ctx, 2) }()
	go func() { results <- dashboard.WaitForReceiptAccepted(ctx) }()
	go func() { results <- dashboard.WaitForSecondState(ctx) }()

	// When
	dashboard.eventMu.Lock()
	dashboard.eventClosed = true
	close(dashboard.eventNotify)
	dashboard.eventNotify = make(chan struct{})
	dashboard.eventMu.Unlock()

	// Then
	for index := 0; index < 4; index++ {
		require.ErrorIs(t, <-results, ErrReceiptGateClosed)
	}
}

func TestDashboardWaitForStateGenerationDoesNotCrossMatchServers(t *testing.T) {
	dashboard := newWaiterDashboard()
	dashboard.stateMu.Lock()
	dashboard.stateEvents[stateEventIdentity{ServerID: 7, UUID: "server-seven", Generation: 1, Count: 1}] = struct{}{}
	dashboard.stateMu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := dashboard.WaitForStateGeneration(ctx, 8, "server-eight", 1, 1)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDashboardAcceptedEventTracksReceiptAndStateGenerationsSeparately(t *testing.T) {
	dashboard := newWaiterDashboard()
	dashboard.receiptEvents = make(chan string)
	dashboard.processReceiptLine("accepted 7 server-seven 4 9 1\n")

	require.Equal(t, uint64(4), dashboard.ReceiptGeneration())
	require.NoError(t, dashboard.WaitForStateGeneration(t.Context(), 7, "server-seven", 9, 1))
}
