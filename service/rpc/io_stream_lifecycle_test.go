package rpc

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type lifecycleRWC struct {
	closed chan struct{}
}

type reenteringErrorRWC struct {
	handler  *NezhaHandler
	streamID string
	err      error
}

func (stream *reenteringErrorRWC) Read([]byte) (int, error)       { return 0, io.EOF }
func (stream *reenteringErrorRWC) Write(data []byte) (int, error) { return len(data), nil }
func (stream *reenteringErrorRWC) Close() error {
	if _, ok := stream.handler.StreamOwnership(stream.streamID); ok {
		return errors.Join(stream.err, errors.New("stream remained registered during endpoint close"))
	}
	return stream.err
}

func newLifecycleRWC() *lifecycleRWC {
	return &lifecycleRWC{closed: make(chan struct{})}
}

func (stream *lifecycleRWC) Read([]byte) (int, error)       { return 0, io.EOF }
func (stream *lifecycleRWC) Write(data []byte) (int, error) { return len(data), nil }
func (stream *lifecycleRWC) Close() error {
	select {
	case <-stream.closed:
	default:
		close(stream.closed)
	}
	return nil
}

func TestIOStreamValidCreateAttachCloseLifecycle(t *testing.T) {
	handler := NewNezhaHandler()
	user := newLifecycleRWC()
	agent := newLifecycleRWC()
	if err := handler.CreateStream("valid-lifecycle", 11, 22); err != nil {
		t.Fatalf("Given a new stream, CreateStream failed: %v", err)
	}
	if err := handler.UserConnected("valid-lifecycle", user); err != nil {
		t.Fatalf("Given a tracked stream, UserConnected failed: %v", err)
	}
	if err := handler.AgentConnected("valid-lifecycle", agent); err != nil {
		t.Fatalf("Given a tracked stream, AgentConnected failed: %v", err)
	}
	if _, ok := handler.StreamOwnership("valid-lifecycle"); !ok {
		t.Fatal("Then a valid attached stream must remain tracked")
	}
	if err := handler.CloseStream("valid-lifecycle"); err != nil {
		t.Fatalf("When closing the valid stream, CloseStream failed: %v", err)
	}
	if _, ok := handler.StreamOwnership("valid-lifecycle"); ok {
		t.Fatal("Then CloseStream must remove the tracked stream")
	}
}

func TestCreateStreamKeepsExistingStreamWhenIDIsDuplicated(t *testing.T) {
	handler := NewNezhaHandler()
	original := newLifecycleRWC()
	if err := handler.CreateStream("duplicate-id", 11, 22); err != nil {
		t.Fatalf("Given a new stream ID, CreateStream failed: %v", err)
	}
	if err := handler.AgentConnected("duplicate-id", original); err != nil {
		t.Fatalf("Given a live stream, AgentConnected failed: %v", err)
	}

	err := handler.CreateStream("duplicate-id", 33, 44)
	if !errors.Is(err, ErrStreamAlreadyExists) {
		t.Fatalf("When reusing a live ID, expected ErrStreamAlreadyExists, got %v", err)
	}
	owner, found := handler.StreamOwnership("duplicate-id")
	if !found || owner != 11 {
		t.Fatalf("Then the original stream ownership must remain, found=%v owner=%d", found, owner)
	}
	select {
	case <-original.closed:
		t.Fatal("Then duplicate creation must not close the original endpoint")
	default:
	}
}

func TestAgentConnectedRejectsDuplicateEndpointWithoutReplacingLiveRelay(t *testing.T) {
	handler := NewNezhaHandler()
	first := newLifecycleRWC()
	second := newLifecycleRWC()
	if err := handler.CreateStream("agent-once", 11, 22); err != nil {
		t.Fatalf("Given a new stream, CreateStream failed: %v", err)
	}
	if err := handler.AgentConnected("agent-once", first); err != nil {
		t.Fatalf("Given no agent endpoint, AgentConnected failed: %v", err)
	}
	if err := handler.AgentConnected("agent-once", second); !errors.Is(err, ErrAgentStreamAlreadyConnected) {
		t.Fatalf("When attaching a second agent endpoint, expected ErrAgentStreamAlreadyConnected, got %v", err)
	}
	endpoints, err := handler.GetStream("agent-once")
	if err != nil || endpoints.agentIo != first {
		t.Fatalf("Then the first endpoint must remain attached, err=%v", err)
	}
	select {
	case <-second.closed:
	default:
		t.Fatal("Then the rejected duplicate endpoint must be closed")
	}
}

func TestCloseStreamWakesWaitForAgentAndAllowsSlotReuse(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("wait-close", 11, 22); err != nil {
		t.Fatalf("Given a pending stream, CreateStream failed: %v", err)
	}
	stream, err := handler.GetStream("wait-close")
	if err != nil {
		t.Fatalf("Given a created stream, GetStream failed: %v", err)
	}
	result := make(chan bool, 1)
	go func() {
		_, ok := handler.WaitForAgent(context.Background(), "wait-close", time.Minute)
		result <- ok
	}()
	select {
	case <-stream.waitStartedCh:
	case <-time.After(time.Second):
		t.Fatal("WaitForAgent did not enter its blocking select")
	}

	if err := handler.CloseStream("wait-close"); err != nil {
		t.Fatalf("When closing a pending stream, CloseStream failed: %v", err)
	}
	select {
	case ok := <-result:
		if ok {
			t.Fatal("Then WaitForAgent must report no attached agent")
		}
	case <-time.After(time.Second):
		t.Fatal("Then CloseStream must wake WaitForAgent")
	}
	if err := handler.CreateStream("wait-close-reused", 11, 22); err != nil {
		t.Fatalf("Then the released user/server slot must be reusable: %v", err)
	}
}

func TestRevokeStreamsForPurposeWakesWaitForAgentAndIsRepeatable(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStreamWithPurpose("revoke-wait", 0, 22, PurposeMCPTransfer); err != nil {
		t.Fatalf("Given a pending MCP stream, CreateStream failed: %v", err)
	}
	stream, err := handler.GetStream("revoke-wait")
	if err != nil {
		t.Fatalf("Given a created stream, GetStream failed: %v", err)
	}
	result := make(chan bool, 1)
	go func() {
		_, ok := handler.WaitForAgent(context.Background(), "revoke-wait", time.Minute)
		result <- ok
	}()
	select {
	case <-stream.waitStartedCh:
	case <-time.After(time.Second):
		t.Fatal("WaitForAgent did not enter its blocking select")
	}

	if revoked := handler.RevokeStreamsForPurpose(PurposeMCPTransfer); revoked != 1 {
		t.Fatalf("When revoking the purpose, expected one stream, got %d", revoked)
	}
	if revoked := handler.RevokeStreamsForPurpose(PurposeMCPTransfer); revoked != 0 {
		t.Fatalf("When repeating revocation, expected zero streams, got %d", revoked)
	}
	select {
	case ok := <-result:
		if ok {
			t.Fatal("Then WaitForAgent must report no attached agent")
		}
	case <-time.After(time.Second):
		t.Fatal("Then revocation must wake WaitForAgent")
	}
}

func TestCloseStreamDetachesBeforeReenteringEndpointCloseAndJoinsErrors(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("close-errors", 1, 1); err != nil {
		t.Fatal(err)
	}
	firstErr := errors.New("first close error")
	secondErr := errors.New("second close error")
	if err := handler.UserConnected("close-errors", &reenteringErrorRWC{handler: handler, streamID: "close-errors", err: firstErr}); err != nil {
		t.Fatal(err)
	}
	if err := handler.AgentConnected("close-errors", &reenteringErrorRWC{handler: handler, streamID: "close-errors", err: secondErr}); err != nil {
		t.Fatal(err)
	}
	err := handler.CloseStream("close-errors")
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("close errors were not joined: %v", err)
	}
}

func TestStartStreamReturnsImmediatelyWhenRevoked(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("start-revoked", 1, 1); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- handler.StartStream("start-revoked", time.Minute) }()
	if revoked := handler.RevokeStreamsForPurpose(PurposeLegacy); revoked != 1 {
		t.Fatalf("revoked streams: %d", revoked)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("revoked StartStream must return an error")
		}
	case <-time.After(time.Second):
		t.Fatal("StartStream did not wake on revoke")
	}
}

func TestConcurrentCloseAndRevokePublishOneGeneration(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("single-generation", 1, 1); err != nil {
		t.Fatal(err)
	}
	start := handler.SnapshotIOStreamState()
	closeDone := make(chan struct{})
	revokeDone := make(chan struct{})
	go func() {
		_ = handler.CloseStream("single-generation")
		close(closeDone)
	}()
	go func() {
		handler.RevokeStreamsForPurpose(PurposeLegacy)
		close(revokeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("CloseStream did not complete")
	}
	select {
	case <-revokeDone:
	case <-time.After(time.Second):
		t.Fatal("RevokeStreamsForPurpose did not complete")
	}
	state := handler.SnapshotIOStreamState()
	if state.Count != 0 || state.Generation != start.Generation+1 {
		t.Fatalf("single detach publication: start=%+v final=%+v", start, state)
	}
}
