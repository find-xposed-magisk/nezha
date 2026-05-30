package grpcx

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nezhahq/nezha/proto"
)

type fakeStream struct {
	frames []*proto.IOStreamData
	err    error
}

func (f *fakeStream) Recv() (*proto.IOStreamData, error) {
	if len(f.frames) == 0 {
		if f.err != nil {
			return nil, f.err
		}
		return nil, io.EOF
	}
	frame := f.frames[0]
	f.frames = f.frames[1:]
	return frame, nil
}

func (f *fakeStream) Send(*proto.IOStreamData) error { return nil }
func (f *fakeStream) Context() context.Context       { return context.Background() }

// Heartbeat frames sent by the agent (ioStreamKeepAlive in
// agent/cmd/agent/main.go) carry an empty Data. The previous wrapper
// surfaced them to callers as (n=0, nil), which made
// mcp_transfer.readXferFixedHeader return "frame too short". This test
// pins the contract: empty frames are transparently skipped and Read
// only returns when it has either real bytes or an error.
func TestIOStreamWrapper_ReadSkipsHeartbeats(t *testing.T) {
	stream := &fakeStream{
		frames: []*proto.IOStreamData{
			{Data: []byte{}},
			{Data: []byte{}},
			{Data: []byte("hello")},
		},
	}
	iw := NewIOStreamWrapper(stream)
	buf := make([]byte, 16)
	n, err := iw.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 || string(buf[:n]) != "hello" {
		t.Fatalf("expected 5 bytes 'hello', got n=%d data=%q", n, buf[:n])
	}
}

// A stream that only ever sends heartbeats followed by an error must
// surface the error rather than spin forever or hand the caller (0, nil).
func TestIOStreamWrapper_ReadPropagatesErrorAfterHeartbeats(t *testing.T) {
	wantErr := errors.New("stream closed")
	stream := &fakeStream{
		frames: []*proto.IOStreamData{
			{Data: []byte{}},
			{Data: []byte{}},
		},
		err: wantErr,
	}
	iw := NewIOStreamWrapper(stream)
	buf := make([]byte, 8)
	n, err := iw.Read(buf)
	if err == nil {
		t.Fatalf("expected error after heartbeats + Recv failure")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error %v, got %v", wantErr, err)
	}
	if n != 0 {
		t.Fatalf("expected n=0 on error, got %d", n)
	}
}

// Close() must wake anything waiting on Done() immediately so co-running
// goroutines (e.g. the dashboard's IOStream keepalive ticker) can exit
// without waiting for the underlying gRPC stream context to cancel or for
// their next Send to fail.
func TestIOStreamWrapper_DoneFiresOnClose(t *testing.T) {
	iw := NewIOStreamWrapper(&fakeStream{})
	select {
	case <-iw.Done():
		t.Fatalf("Done() must not fire before Close()")
	default:
	}
	if err := iw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-iw.Done():
	case <-time.After(time.Second):
		t.Fatalf("Done() did not fire after Close()")
	}
	// Idempotent: a second Close must not panic on the closed channel.
	if err := iw.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
