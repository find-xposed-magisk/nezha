package rpc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
)

// Kill switch must beat a late agent reply. Without the cancelled-flag
// re-check in CallAgent, the following sequence surfaces success after
// EnableMCP=false:
//
//	t0  agent puts TaskResult into resultCh (capacity 1, non-blocking)
//	t1  admin flips EnableMCP=false → CancelAllMCPInflight closes cancelCh
//	t2  CallAgent's select sees BOTH cases ready; Go picks one at random;
//	    if it picks resultCh, the call returns the agent's payload even
//	    though the operator's kill switch fired.
//
// The fix is to mark the entry cancelled BEFORE closing cancelCh and have
// the resultCh branch re-check that flag. This test pins the contract by
// driving the worst-case ordering: result is delivered FIRST, then the
// kill switch fires, then CallAgent observes both. With the race in place
// this would flake (random select); with the fix it always returns
// ErrMCPDisabled.
func TestCallAgent_KillSwitchBeatsConcurrentLateResult(t *testing.T) {
	const target uint64 = 7301

	stream := newFakeStream()
	cleanup := installFakeServer(t, target, stream)
	defer cleanup()

	resultSelected := make(chan struct{})
	resumeResult := make(chan struct{})
	var resultHook atomic.Pointer[func()]
	hook := func() {
		close(resultSelected)
		<-resumeResult
	}
	resultHook.Store(&hook)
	testMCPResultBeforeCancellationCheck.Store(resultHook.Load())
	t.Cleanup(func() { testMCPResultBeforeCancellationCheck.Store(nil) })

	delivered := make(chan struct{})
	go func() {
		sent := <-stream.sent
		deliverMCPResultFromReporter(&pb.TaskResult{
			Id:         sent.GetId(),
			Type:       model.TaskTypeExec,
			Successful: true,
			Data:       `{"exit_code":0,"stdout":"should-not-surface"}`,
		}, target)
		close(delivered)
	}()

	errCh := make(chan error, 1)
	go func() {
		_, err := CallAgent(context.Background(), target, model.TaskTypeExec,
			model.ExecRequest{Cmd: "x"}, 2*time.Second)
		errCh <- err
	}()
	<-resultSelected
	CancelAllMCPInflight()
	close(resumeResult)
	<-delivered
	err := <-errCh
	if !errors.Is(err, ErrMCPDisabled) {
		t.Fatalf("kill switch must win the race with a late agent reply; want ErrMCPDisabled, got %v", err)
	}
}

func TestCallAgent_ResultBeforeKillSwitchReturnsSuccess(t *testing.T) {
	const target uint64 = 7303

	stream := newFakeStream()
	cleanup := installFakeServer(t, target, stream)
	defer cleanup()

	resultClaimed := make(chan struct{})
	resumeResult := make(chan struct{})
	var resultHook atomic.Pointer[func()]
	hook := func() {
		close(resultClaimed)
		<-resumeResult
	}
	resultHook.Store(&hook)
	testMCPResultAfterCancellationCheck.Store(resultHook.Load())
	t.Cleanup(func() { testMCPResultAfterCancellationCheck.Store(nil) })

	go func() {
		sent := <-stream.sent
		deliverMCPResultFromReporter(&pb.TaskResult{
			Id:         sent.GetId(),
			Type:       model.TaskTypeExec,
			Successful: true,
			Data:       `{"exit_code":0}`,
		}, target)
	}()

	errCh := make(chan error, 1)
	go func() {
		_, err := CallAgent(context.Background(), target, model.TaskTypeExec,
			model.ExecRequest{Cmd: "x"}, 2*time.Second)
		errCh <- err
	}()
	<-resultClaimed
	CancelAllMCPInflight()
	close(resumeResult)
	if err := <-errCh; err != nil {
		t.Fatalf("result claimed before kill switch must succeed, got %v", err)
	}
}

// CancelAllMCPInflight must eagerly evict entries so a stale TaskResult
// that arrives after the kill switch cannot still land in resultCh.
// Without the cancelled flag this entry would still be reachable through
// deliverMCPResultFromReporter; the flag guarantees the late delivery is
// silently dropped even if the caller has not returned yet.
func TestCancelAllMCPInflight_LaterResultIsSwallowed(t *testing.T) {
	const target uint64 = 7302

	stream := newFakeStream()
	cleanup := installFakeServer(t, target, stream)
	defer cleanup()

	taskIDCh := make(chan uint64, 1)
	go func() {
		sent := <-stream.sent
		taskIDCh <- sent.GetId()
	}()

	resultCh := make(chan error, 1)
	go func() {
		_, err := CallAgent(context.Background(), target, model.TaskTypeExec,
			model.ExecRequest{Cmd: "x"}, 5*time.Second)
		resultCh <- err
	}()

	taskID := <-taskIDCh
	CancelAllMCPInflight()

	if err := <-resultCh; !errors.Is(err, ErrMCPDisabled) {
		t.Fatalf("CallAgent must return ErrMCPDisabled after kill switch; got %v", err)
	}

	deliverMCPResultFromReporter(&pb.TaskResult{
		Id:         taskID,
		Type:       model.TaskTypeExec,
		Successful: true,
		Data:       `{"exit_code":0}`,
	}, target)
}
