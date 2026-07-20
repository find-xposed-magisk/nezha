package rpc

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWaitForIOStreamStateWakesOnCloseAndAbsence(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("wait-state", 1, 1); err != nil {
		t.Fatal(err)
	}
	result := make(chan IOStreamState, 1)
	go func() {
		state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0), AbsentStreamID: "wait-state"})
		if err != nil {
			t.Errorf("wait failed: %v", err)
			return
		}
		result <- state
	}()
	if err := handler.CloseStream("wait-state"); err != nil {
		t.Fatal(err)
	}
	state := <-result
	if state.Count != 0 || state.Generation != 2 {
		t.Fatalf("unexpected waited state: %+v", state)
	}
}

func TestWaitForIOStreamStateCreateWakeUsesCapturedNotification(t *testing.T) {
	handler := NewNezhaHandler()
	waitReady := make(chan struct{})
	handler.ioStreamWaitLockedHook = func() {
		select {
		case <-waitReady:
		default:
			close(waitReady)
		}
	}
	result := make(chan IOStreamState, 1)
	go func() {
		state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1)})
		if err == nil {
			result <- state
		}
	}()
	select {
	case <-waitReady:
	case <-time.After(time.Second):
		t.Fatal("waiter did not capture its notification channel")
	}
	if err := handler.CreateStream("create-wake", 1, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-result:
		if state.Count != 1 || state.Generation != 1 {
			t.Fatalf("unexpected created state: %+v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("create did not wake waiter")
	}
}

func TestWaitForIOStreamStateCloseWakeUsesCapturedNotification(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("close-wake", 1, 1); err != nil {
		t.Fatal(err)
	}
	waitReady := make(chan struct{})
	handler.ioStreamWaitLockedHook = func() {
		select {
		case <-waitReady:
		default:
			close(waitReady)
		}
	}
	result := make(chan IOStreamState, 1)
	go func() {
		state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0), AbsentStreamID: "close-wake"})
		if err == nil {
			result <- state
		}
	}()
	select {
	case <-waitReady:
	case <-time.After(time.Second):
		t.Fatal("waiter did not capture its notification channel")
	}
	if err := handler.CloseStream("close-wake"); err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-result:
		if state.Count != 0 || state.Generation != 2 {
			t.Fatalf("unexpected closed state: %+v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake waiter")
	}
}

func TestWaitForIOStreamStateDoesNotMissMutationBetweenSnapshotAndWait(t *testing.T) {
	handler := NewNezhaHandler()
	hookCalled := make(chan struct{})
	mutationDone := make(chan error, 1)
	var hookOnce sync.Once
	handler.ioStreamWaitLockedHook = func() {
		hookOnce.Do(func() {
			close(hookCalled)
			go func() {
				mutationDone <- handler.CreateStream("lost-wakeup", 1, 1)
			}()
		})
	}
	result := make(chan IOStreamState, 1)
	go func() {
		state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1)})
		if err == nil {
			result <- state
		}
	}()
	select {
	case <-hookCalled:
	case <-time.After(time.Second):
		t.Fatal("waiter did not reach deterministic mutation seam")
	}
	select {
	case err := <-mutationDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("mutation did not complete")
	}
	select {
	case state := <-result:
		if state.Count != 1 || state.Generation != 1 {
			t.Fatalf("unexpected mutation state: %+v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter missed mutation published during wait setup")
	}
}

func TestWaitForIOStreamStateConcurrentCreateCloseWaiters(t *testing.T) {
	handler := NewNezhaHandler()
	created := make(chan IOStreamState, 1)
	closed := make(chan IOStreamState, 1)
	go func() {
		state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1)})
		if err == nil {
			created <- state
		}
	}()
	if err := handler.CreateStream("concurrent", 1, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-created:
		if state.Count != 1 {
			t.Fatalf("created waiter state: %+v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("created waiter did not wake")
	}
	go func() {
		state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0), AbsentStreamID: "concurrent"})
		if err == nil {
			closed <- state
		}
	}()
	if err := handler.CloseStream("concurrent"); err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-closed:
		if state.Count != 0 {
			t.Fatalf("closed waiter state: %+v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("closed waiter did not wake")
	}
}

func TestWaitForIOStreamStateDoesNotAcceptUnrelatedSameCountForPresentID(t *testing.T) {
	handler := NewNezhaHandler()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := handler.WaitForIOStreamState(ctx, IOStreamStateExpectation{
			ExpectedCount:   ExpectedIOStreamCount(1),
			PresentStreamID: "wanted",
		})
		result <- err
	}()
	if err := handler.CreateStream("unrelated", 1, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("same-count unrelated stream satisfied identity expectation")
		}
	default:
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("identity waiter unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("identity waiter did not observe cancellation")
	}
}
