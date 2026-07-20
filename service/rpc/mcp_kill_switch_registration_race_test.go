package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
)

// Registration-after-sweep race (review issue #1): CancelAllMCPInflight only
// cancels entries already present in the inflight map. A CallAgent that passes
// the upfront kill-switch check but has not yet Store()d its entry is invisible
// to the sweep, so without a post-registration re-check it goes on to SendTask
// a fresh exec/fs task to the agent AFTER EnableMCP=false.
//
// This test drives the worst-case interleaving deterministically:
//  1. CallAgent passes the upfront observer check (observer still false).
//  2. The operator flips the observer to "disabled" and runs the cancel sweep
//     while CallAgent is paused between the check and Store.
//  3. CallAgent resumes; it MUST observe the kill switch on the post-Store
//     re-check and return ErrMCPDisabled WITHOUT sending the task.
//
// With the race present, CallAgent sends the task and blocks until timeout
// (ErrAgentTimeout) — the agent received a fresh task past the kill switch.
func TestCallAgent_KillSwitchBeatsRegistrationAfterSweep(t *testing.T) {
	const target uint64 = 7401

	stream := newFakeStream()
	cleanup := installFakeServer(t, target, stream)
	defer cleanup()

	var killed bool
	var mu sync.Mutex
	prev := mcpKillSwitchObserver()
	// Observer returns the operator-controlled flag. CallAgent reads it both
	// before and (with the fix) after registering the inflight entry.
	SetMCPKillSwitchObserver(func() bool {
		mu.Lock()
		defer mu.Unlock()
		return killed
	})
	t.Cleanup(func() { SetMCPKillSwitchObserver(prev) })

	// Fail loudly if the agent ever receives a task: that means a fresh call
	// leaked past the kill switch.
	leaked := make(chan *struct{}, 1)
	go func() {
		select {
		case <-stream.sent:
			leaked <- nil
		case <-time.After(2 * time.Second):
		}
	}()

	// Arrange the interleaving: hook fires once CallAgent is about to register,
	// flipping the kill switch and running the cancel sweep so the not-yet-Stored
	// entry is missed by the sweep.
	hook := func() {
		mu.Lock()
		killed = true
		mu.Unlock()
		CancelAllMCPInflight()
	}
	testKillSwitchAfterUpfrontCheck.Store(&hook)
	t.Cleanup(func() { testKillSwitchAfterUpfrontCheck.Store(nil) })

	_, err := CallAgent(context.Background(), target, model.TaskTypeExec,
		model.ExecRequest{Cmd: "x"}, 1*time.Second)

	if !errors.Is(err, ErrMCPDisabled) {
		t.Fatalf("CallAgent must return ErrMCPDisabled when the kill switch fires during registration; got %v", err)
	}
	select {
	case <-leaked:
		t.Fatal("a fresh MCP task leaked to the agent past the kill switch")
	default:
	}
}
