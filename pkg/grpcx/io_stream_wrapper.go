package grpcx

import (
	"context"
	"io"
	"sync"
	"sync/atomic"

	"github.com/nezhahq/nezha/proto"
)

var _ io.ReadWriteCloser = (*IOStreamWrapper)(nil)

type IOStream interface {
	Recv() (*proto.IOStreamData, error)
	Send(*proto.IOStreamData) error
	Context() context.Context
}

// IOStreamWrapper adapts a gRPC IOStream into an io.ReadWriteCloser and
// serializes every Send on the underlying stream. grpc-go forbids concurrent
// SendMsg on the same stream (Documentation/concurrency.md); the dashboard
// runs an IOStream keepalive goroutine alongside MCP fs.transfer / terminal /
// fm Writers, so all of them must funnel through this sendMu. The matching
// agent-side fix is serialIOStreamSender in agent/cmd/agent/mcp_fs_transfer.go.
type IOStreamWrapper struct {
	IOStream
	sendMu  sync.Mutex
	dataBuf []byte
	closed  *atomic.Bool
	closeCh chan struct{}
}

func NewIOStreamWrapper(stream IOStream) *IOStreamWrapper {
	return &IOStreamWrapper{
		IOStream: stream,
		closeCh:  make(chan struct{}),
		closed:   new(atomic.Bool),
	}
}

// Send writes a single IOStreamData frame under the wrapper's send mutex.
// All goroutines that share this wrapper — keepalive ticker, Write callers,
// and any direct frame writer — MUST go through Send (or SendKeepalive)
// rather than touching the embedded IOStream.Send, otherwise grpc-go's
// concurrent-SendMsg invariant is violated and frames can corrupt or panic.
func (iw *IOStreamWrapper) Send(data *proto.IOStreamData) error {
	iw.sendMu.Lock()
	defer iw.sendMu.Unlock()
	return iw.IOStream.Send(data)
}

// SendKeepalive sends the dashboard's empty-payload heartbeat through the
// same sendMu as Send/Write so it cannot race the data path.
func (iw *IOStreamWrapper) SendKeepalive() error {
	return iw.Send(&proto.IOStreamData{Data: []byte{}})
}

// RecvFrame returns the next non-empty IOStream frame as a single contiguous
// byte slice, preserving frame boundaries. Use this when a caller multiplexes
// control frames (magic + payload) and data frames over the same stream and
// must not let one frame's bytes spill into the next frame's parsing.
//
// The io.Reader path (Read) intentionally hides frame boundaries; callers that
// need them — e.g. MCP fs.transfer download where NZTE may interrupt NZTD
// payload mid-stream — call RecvFrame instead.
func (iw *IOStreamWrapper) RecvFrame() ([]byte, error) {
	if len(iw.dataBuf) > 0 {
		out := iw.dataBuf
		iw.dataBuf = nil
		return out, nil
	}
	for {
		data, err := iw.Recv()
		if err != nil {
			return nil, err
		}
		if len(data.Data) == 0 {
			continue
		}
		return data.Data, nil
	}
}

func (iw *IOStreamWrapper) Read(p []byte) (n int, err error) {
	if len(iw.dataBuf) > 0 {
		n := copy(p, iw.dataBuf)
		iw.dataBuf = iw.dataBuf[n:]
		return n, nil
	}
	// Skip zero-length heartbeat frames sent by ioStreamKeepAlive (see
	// agent/cmd/agent/main.go ioStreamKeepAlive). protobuf treats an empty
	// `bytes` field as a default value but still ships a valid Message, so
	// Recv() returns a non-nil *IOStreamData whose Data is empty. Surfacing
	// that as (0, nil) is legal io.Reader behaviour but every caller in the
	// repo treats a 0-byte read as an unexpected control frame (e.g.
	// mcp_transfer.readXferFixedHeader returns "frame too short"). Loop here
	// until we get either real bytes or an error.
	for {
		var data *proto.IOStreamData
		if data, err = iw.Recv(); err != nil {
			return 0, err
		}
		if len(data.Data) == 0 {
			continue
		}
		n = copy(p, data.Data)
		if n < len(data.Data) {
			iw.dataBuf = data.Data[n:]
		}
		return n, nil
	}
}

func (iw *IOStreamWrapper) Write(p []byte) (n int, err error) {
	err = iw.Send(&proto.IOStreamData{Data: p})
	return len(p), err
}

func (iw *IOStreamWrapper) Close() error {
	if iw.closed.CompareAndSwap(false, true) {
		close(iw.closeCh)
		if closer, ok := iw.IOStream.(interface{ Close() error }); ok {
			return closer.Close()
		}
	}
	return nil
}

func (iw *IOStreamWrapper) Wait() {
	<-iw.closeCh
}

// Done exposes the wrapper's close signal as a read-only channel so callers
// that run alongside the wrapper (e.g. the dashboard's IOStream keepalive
// goroutine) can cancel cooperatively. Without this they would only stop on
// gRPC stream-context cancel or on their next failed Send, which can leave
// a goroutine waiting up to one keepalive tick after the wrapper was closed.
func (iw *IOStreamWrapper) Done() <-chan struct{} {
	return iw.closeCh
}
