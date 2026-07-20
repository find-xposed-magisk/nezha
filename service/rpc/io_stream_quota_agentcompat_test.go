//go:build agentcompat

package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestAgentCompatIOStreamQuotaProbe(t *testing.T) {
	result := RunIOStreamQuotaProbe(context.Background())
	if result.Err != nil {
		t.Fatalf("quota probe failed: %v", result.Err)
	}
	if result.UserAccepted != maxStreamsPerUser || result.UserRejected != 1 {
		t.Fatalf("unexpected user boundary counts: accepted=%d rejected=%d", result.UserAccepted, result.UserRejected)
	}
	if result.ServerAccepted != maxStreamsPerServer || result.ServerRejected != 1 {
		t.Fatalf("unexpected server boundary counts: accepted=%d rejected=%d", result.ServerAccepted, result.ServerRejected)
	}
	if result.TrackedStreams != 0 {
		t.Fatalf("probe left tracked streams: %d", result.TrackedStreams)
	}
	if !result.WaitForAgentWokeOnClose {
		t.Fatal("probe did not prove WaitForAgent wakes after real stream close")
	}
	if !result.UserSlotReused {
		t.Fatal("probe did not prove a released user slot was reusable")
	}
}

func TestAgentCompatIOStreamQuotaProbeUsesProductionSeam(t *testing.T) {
	result := RunIOStreamQuotaProbe(context.Background())
	if !errors.Is(result.UserBoundaryError, ErrTooManyStreamsForUser) {
		t.Fatalf("user rejection must preserve production error, got %v", result.UserBoundaryError)
	}
	if !errors.Is(result.ServerBoundaryError, ErrTooManyStreamsForServer) {
		t.Fatalf("server rejection must preserve production error, got %v", result.ServerBoundaryError)
	}
}

func TestAgentCompatIOStreamQuotaProbeConcurrentBoundaryCalls(t *testing.T) {
	h := NewNezhaHandler()
	const userID, serverID = uint64(701), uint64(901)
	var wg sync.WaitGroup
	results := make(chan error, maxStreamsPerUser+1)
	for i := 0; i < maxStreamsPerUser+1; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results <- h.CreateStream(fmt.Sprintf("concurrent-user-%d", index), userID, serverID+uint64(index))
		}(i)
	}
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		if errors.Is(err, ErrTooManyStreamsForUser) {
			rejected++
			continue
		}
		t.Fatalf("unexpected concurrent boundary error: %v", err)
	}
	if accepted != maxStreamsPerUser || rejected != 1 {
		t.Fatalf("unexpected concurrent boundary counts: accepted=%d rejected=%d", accepted, rejected)
	}
	for i := 0; i < maxStreamsPerUser+1; i++ {
		_ = h.CloseStream(fmt.Sprintf("concurrent-user-%d", i))
	}
}
