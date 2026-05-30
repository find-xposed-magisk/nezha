package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
)

// These tests pin the security invariant that an MCP TaskResult delivered
// back through RequestTask must come from the SAME agent the CallAgent was
// targeted at. The receive loop in service/rpc/nezha.go has the authenticated
// clientID in scope; deliverMCPResult must consume it and reject mismatches.
//
// Why the invariant matters: mcpInflight is keyed by a globally increasing
// counter (allocateMCPTaskID) and the lookup table is shared across servers.
// Without binding the inflight entry to the target serverID and verifying it
// against the reporter clientID, any compromised agent A can race a forged
// TaskResult for server B's CallAgent (resultCh capacity is 1; first reply
// wins, real reply is dropped). The same class of attack motivated the cron
// path's CanReportCronResult and the transfer path's pending.ID == result.Id
// check in this very file's RequestTask switch.

// TestDeliverMCPResult_RejectsForeignReporter is the security regression: a
// reporter that is NOT the call target must not be able to deliver into
// another server's inflight slot, even with a correctly-guessed taskID.
func TestDeliverMCPResult_RejectsForeignReporter(t *testing.T) {
	const (
		targetServerID uint64 = 6101
		foreignAgentID uint64 = 6102
	)

	stream := newFakeStream()
	cleanup := installFakeServer(t, targetServerID, stream)
	defer cleanup()

	captured := make(chan uint64, 1)
	go func() {
		sent := <-stream.sent
		// Foreign agent racing a forged TaskResult with the right taskID.
		DeliverMCPResultFromReporterForTest(&pb.TaskResult{
			Id:         sent.GetId(),
			Type:       model.TaskTypeExec,
			Successful: true,
			Data:       `{"exit_code":0,"stdout":"forged"}`,
		}, foreignAgentID)
		captured <- sent.GetId()
	}()

	_, err := CallAgent(context.Background(), targetServerID, model.TaskTypeExec,
		model.ExecRequest{Cmd: "x"}, 200*time.Millisecond)
	if !errors.Is(err, ErrAgentTimeout) {
		t.Fatalf("forged result from foreign reporter must NOT deliver; want ErrAgentTimeout, got %v", err)
	}
	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatalf("test stream never observed the dispatched task")
	}
}

// TestDeliverMCPResult_AcceptsMatchingReporter is the green companion: when
// the reporter clientID matches the inflight target, the result must still
// route correctly (we are not breaking the happy path).
func TestDeliverMCPResult_AcceptsMatchingReporter(t *testing.T) {
	const targetServerID uint64 = 6103

	stream := newFakeStream()
	cleanup := installFakeServer(t, targetServerID, stream)
	defer cleanup()

	want := model.ExecResult{ExitCode: 0, Stdout: "ok"}
	payload, _ := json.Marshal(want)

	go func() {
		sent := <-stream.sent
		DeliverMCPResultFromReporterForTest(&pb.TaskResult{
			Id:         sent.GetId(),
			Type:       model.TaskTypeExec,
			Successful: true,
			Data:       string(payload),
		}, targetServerID)
	}()

	raw, err := CallAgent(context.Background(), targetServerID, model.TaskTypeExec,
		model.ExecRequest{Cmd: "x"}, 2*time.Second)
	if err != nil {
		t.Fatalf("matching reporter must deliver, got %v", err)
	}
	var got model.ExecResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("bad result json: %v", err)
	}
	if got.Stdout != "ok" {
		t.Fatalf("payload not propagated, got %+v", got)
	}
}

// TestDeliverMCPResult_InflightEntryBoundToServerID locks in the structural
// requirement that the inflight table records the target serverID. Without
// this binding deliverMCPResult cannot perform the reporter check above.
// Probing via reflection avoids exporting mcpInflight just for tests.
func TestDeliverMCPResult_InflightEntryBoundToServerID(t *testing.T) {
	const targetServerID uint64 = 6104

	stream := newFakeStream()
	cleanup := installFakeServer(t, targetServerID, stream)
	defer cleanup()

	gotEntry := make(chan struct {
		taskID   uint64
		serverID uint64
		found    bool
	}, 1)
	go func() {
		sent := <-stream.sent
		taskID := sent.GetId()
		serverID, ok := inflightServerIDForTest(taskID)
		gotEntry <- struct {
			taskID   uint64
			serverID uint64
			found    bool
		}{taskID, serverID, ok}
		// Unblock CallAgent so the inflight slot is cleaned up.
		DeliverMCPResultFromReporterForTest(&pb.TaskResult{
			Id:         taskID,
			Type:       model.TaskTypeExec,
			Successful: true,
			Data:       "{}",
		}, targetServerID)
	}()

	_, err := CallAgent(context.Background(), targetServerID, model.TaskTypeExec,
		model.ExecRequest{Cmd: "x"}, 2*time.Second)
	if err != nil {
		t.Fatalf("unexpected CallAgent error: %v", err)
	}
	probe := <-gotEntry
	if !probe.found {
		t.Fatalf("inflight entry for taskID=%d not found while CallAgent was blocking", probe.taskID)
	}
	if probe.serverID != targetServerID {
		t.Fatalf("inflight entry must carry target serverID=%d, got %d", targetServerID, probe.serverID)
	}
}
