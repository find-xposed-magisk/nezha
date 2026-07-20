//go:build agentcompat

package rpc

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWaitForIOStreamStateObserverCanReenterWriteLock(t *testing.T) {
	handler := NewNezhaHandler()
	observerCalled := make(chan struct{})
	var observerOnce sync.Once
	handler.SetIOStreamStateWaitObserverForAgentcompat(func() {
		observerOnce.Do(func() {
			if err := handler.CreateStreamWithPurpose("observer-reentrant", 1, 1, PurposeLegacy); err != nil {
				t.Errorf("observer create: %v", err)
			}
			close(observerCalled)
		})
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := handler.WaitForIOStreamState(ctx, IOStreamStateExpectation{PresentStreamID: "observer-reentrant"})
		result <- err
	}()
	select {
	case <-observerCalled:
	case <-ctx.Done():
		t.Fatalf("observer remained blocked by read lock: %v", ctx.Err())
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("wait after reentrant observer: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("wait did not observe observer mutation: %v", ctx.Err())
	}
}

func TestWaitForIOStreamStateObserverMutationWakesCapturedNotification(t *testing.T) {
	handler := NewNezhaHandler()
	observerCalled := make(chan struct{})
	var observerOnce sync.Once
	handler.SetIOStreamStateWaitObserverForAgentcompat(func() {
		observerOnce.Do(func() {
			if err := handler.CreateStreamWithPurpose("observer-wake", 1, 1, PurposeLegacy); err != nil {
				t.Errorf("observer create: %v", err)
			}
			close(observerCalled)
		})
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := handler.WaitForIOStreamState(ctx, IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1)})
	if err != nil {
		t.Fatalf("wait after observer mutation: %v", err)
	}
	select {
	case <-observerCalled:
	default:
		t.Fatal("observer did not run")
	}
	if state.Count != 1 {
		t.Fatalf("observer mutation state: %+v", state)
	}
}

func TestWaitForIOStreamStateNoOpObserverKeepsMutationBetweenSnapshotAndSelect(t *testing.T) {
	handler := NewNezhaHandler()
	observerCalled := make(chan struct{})
	var observerOnce sync.Once
	handler.SetIOStreamStateWaitObserverForAgentcompat(func() {
		observerOnce.Do(func() { close(observerCalled) })
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan IOStreamState, 1)
	resultErr := make(chan error, 1)
	go func() {
		state, err := handler.WaitForIOStreamState(ctx, IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1)})
		if err != nil {
			resultErr <- err
			return
		}
		result <- state
	}()
	select {
	case <-observerCalled:
	case <-ctx.Done():
		t.Fatalf("observer did not run: %v", ctx.Err())
	}
	if err := handler.CreateStreamWithPurpose("observer-noop-create", 1, 1, PurposeLegacy); err != nil {
		t.Fatalf("mutation between snapshot and select: %v", err)
	}
	select {
	case state := <-result:
		if state.Count != 1 {
			t.Fatalf("mutation state: %+v", state)
		}
	case err := <-resultErr:
		t.Fatalf("wait after no-op observer mutation: %v", err)
	case <-ctx.Done():
		t.Fatalf("wait missed mutation after no-op observer: %v", ctx.Err())
	}
}
