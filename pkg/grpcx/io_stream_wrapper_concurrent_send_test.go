package grpcx

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nezhahq/nezha/proto"
)

// sendObservingStream records the maximum number of goroutines that are
// inside Send at the same time. grpc-go's real server stream is NOT safe
// under concurrent Send, but this fake never blocks so any concurrent
// dispatch from the wrapper would surface here as maxInFlight > 1.
type sendObservingStream struct {
	inFlight    int32
	maxInFlight int32
}

func (s *sendObservingStream) Recv() (*proto.IOStreamData, error) { return nil, nil }
func (s *sendObservingStream) Context() context.Context           { return context.Background() }
func (s *sendObservingStream) Send(*proto.IOStreamData) error {
	cur := atomic.AddInt32(&s.inFlight, 1)
	defer atomic.AddInt32(&s.inFlight, -1)
	for {
		prev := atomic.LoadInt32(&s.maxInFlight)
		if cur <= prev || atomic.CompareAndSwapInt32(&s.maxInFlight, prev, cur) {
			break
		}
	}
	return nil
}

// IOStreamWrapper.Send and SendKeepalive must be safe to call from many
// goroutines concurrently — this is the dashboard-side dual of the agent's
// serialIOStreamSender (see agent/cmd/agent/mcp_fs_transfer.go). Without the
// wrapper's sendMu, dashboard IOStream keepalive + MCP fs.transfer Write
// race the same gRPC stream, violating grpc-go's "no concurrent SendMsg"
// contract. We pin that with a stress test: many goroutines hammer Send /
// SendKeepalive / Write at once; the fake stream must NEVER observe more
// than one in-flight Send.
func TestIOStreamWrapper_SerializesConcurrentSends(t *testing.T) {
	obs := &sendObservingStream{}
	iw := NewIOStreamWrapper(obs)

	const workers = 16
	const opsPerWorker = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < opsPerWorker; j++ {
				switch (seed + j) % 3 {
				case 0:
					_ = iw.Send(&proto.IOStreamData{Data: []byte{byte(seed)}})
				case 1:
					_ = iw.SendKeepalive()
				case 2:
					_, _ = iw.Write([]byte{byte(j)})
				}
			}
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&obs.maxInFlight); got != 1 {
		t.Fatalf("IOStreamWrapper.Send must serialize through sendMu; observed max-in-flight=%d, want 1", got)
	}
}
