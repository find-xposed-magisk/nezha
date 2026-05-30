package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/grpcx"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

func newFakeAgentIO() *grpcx.IOStreamWrapper {
	return grpcx.NewIOStreamWrapper(&fakeAgentStream{closed: make(chan struct{})})
}

// fakeAgentStream mimics an attached-but-silent agent: Recv blocks until the
// wrapper is closed, exactly the post-attach state where nothing watches the
// per-transfer context.
type fakeAgentStream struct {
	closed chan struct{}
}

func (f *fakeAgentStream) Recv() (*pb.IOStreamData, error) {
	<-f.closed
	return nil, context.Canceled
}
func (f *fakeAgentStream) Send(*pb.IOStreamData) error { return nil }
func (f *fakeAgentStream) Context() context.Context    { return context.Background() }

// transferRevokableContext only cancels a context; the post-attach relay
// (readXferFixedHeader / relayDownloadFrames / io.CopyN) and IOStreamWrapper.Read
// do not watch it. A revoked PAT (or a disconnected HTTP client) must still
// tear down the attached stream, else a stalled/compromised agent pins a
// dashboard goroutine + IOStream until restart. openFsTransferStream must wire
// ctx cancellation to CloseStream.
func TestOpenFsTransferStream_CancelClosesAttachedStream(t *testing.T) {
	cleanupMCP, _ := setupMCPTest(t)
	defer cleanupMCP()
	singleton.Conf.SetMCPEnabled(true)

	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })

	stream := newKillSwitchStream()
	sc := singleton.NewEmptyServerClassForTest()
	srv := &model.Server{}
	srv.ID = 7
	srv.SetTaskStream(stream)
	sc.InsertForTest(srv)
	originalShared := singleton.ServerShared
	singleton.ServerShared = sc
	t.Cleanup(func() { singleton.ServerShared = originalShared })

	streamIDCh := make(chan string, 1)
	go func() {
		task := <-stream.sent
		var req model.FsTransferRequest
		_ = json.Unmarshal([]byte(task.GetData()), &req)
		streamIDCh <- req.StreamID
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if err := rpc.NezhaHandlerSingleton.AgentConnected(req.StreamID, newFakeAgentIO()); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	streamIO, cleanup, err := openFsTransferStream(ctx, 7, &model.FsTransferRequest{
		Op:   model.MCPFsTransferOpDownload,
		Path: "/srv/file",
	})
	require.NoError(t, err, "agent must attach so openFsTransferStream returns a live stream")
	require.NotNil(t, streamIO)
	defer cleanup()

	streamID := <-streamIDCh
	_, getErr := rpc.NezhaHandlerSingleton.GetStream(streamID)
	require.NoError(t, getErr, "stream must be live before cancel")

	cancel()

	require.Eventually(t, func() bool {
		_, e := rpc.NezhaHandlerSingleton.GetStream(streamID)
		return e != nil
	}, 2*time.Second, 10*time.Millisecond,
		"cancelling the transfer context must tear down the attached IOStream")
}
