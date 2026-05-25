package model

import (
	"context"
	"sync"
	"testing"

	pb "github.com/nezhahq/nezha/proto"
)

// raceProbeStream is the smallest fake of pb.NezhaService_RequestTaskServer
// the race probe needs. We only call Send on it from the test; the embedded
// interface satisfies the rest of the contract with nil-panicking methods we
// never invoke.
type raceProbeStream struct {
	pb.NezhaService_RequestTaskServer
}

func (raceProbeStream) Send(*pb.Task) error      { return nil }
func (raceProbeStream) Context() context.Context { return context.Background() }

// model.Server.TaskStream is read from many goroutines (singleton cron pushes,
// transfer ApplyConfig pushes, terminal/fm proxies, dashboard rpc keepalives,
// per-server batch pushes) and written from exactly one (the gRPC RequestTask
// goroutine on every fresh agent connection). The bare-field access pattern
// `if s.TaskStream != nil { s.TaskStream.Send(...) }` is a data race on the
// interface header (two-word value) and can torn-read into a panic on a
// reconnect. This test pins down "concurrent set + send must be race-free"
// using the Go race detector — without the fix, `go test -race` reports a
// data race on TaskStream; with the fix the field is encapsulated behind
// atomic methods and the test runs clean. Without `-race` both versions are
// indistinguishable, so this test is only meaningful under the race flag —
// run it from CI as `go test -race ./model/`.
func TestServerTaskStreamConcurrentAccessIsRaceFree(t *testing.T) {
	s := &Server{}
	InitServer(s)

	const (
		writers = 4
		readers = 8
		rounds  = 200
	)
	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				s.SetTaskStream(raceProbeStream{})
				s.SetTaskStream(nil)
			}
		}()
	}
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				if stream := s.GetTaskStream(); stream != nil {
					_ = stream.Send(nil)
				}
			}
		}()
	}
	wg.Wait()
}

func TestServerClearTaskStreamIfCurrentClearsOnlyMatchingStream(t *testing.T) {
	s := &Server{}
	InitServer(s)

	first := &raceProbeStream{}
	second := &raceProbeStream{}

	s.SetTaskStream(first)
	if !s.ClearTaskStreamIfCurrent(first) {
		t.Fatal("matching current stream must be cleared")
	}
	if got := s.GetTaskStream(); got != nil {
		t.Fatalf("expected cleared task stream, got %T", got)
	}

	s.SetTaskStream(first)
	s.SetTaskStream(second)
	if s.ClearTaskStreamIfCurrent(first) {
		t.Fatal("stale stream cleanup must not clear a newer stream")
	}
	if got := s.GetTaskStream(); got != second {
		t.Fatalf("expected newer stream to remain published, got %T", got)
	}
}
