package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
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
	closed    chan struct{}
	closeOnce sync.Once
	closeSeen chan struct{}
	recvDone  chan struct{}
	closeCall atomic.Int32
}

func (f *fakeAgentStream) Recv() (*pb.IOStreamData, error) {
	<-f.closed
	close(f.recvDone)
	return nil, context.Canceled
}
func (f *fakeAgentStream) Send(*pb.IOStreamData) error { return nil }
func (f *fakeAgentStream) Context() context.Context    { return context.Background() }

func (f *fakeAgentStream) closeEndpoint() {
	f.closeOnce.Do(func() { close(f.closeSeen) })
}

func (f *fakeAgentStream) Close() error {
	f.closeCall.Add(1)
	f.closeEndpoint()
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return nil
}

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
	agentStreamCh := make(chan *fakeAgentStream, 1)
	attachReady := make(chan struct{})
	go func() {
		task := <-stream.sent
		var req model.FsTransferRequest
		require.NoError(t, json.Unmarshal([]byte(task.GetData()), &req))
		streamIDCh <- req.StreamID
		fakeAgent := &fakeAgentStream{closed: make(chan struct{}), closeSeen: make(chan struct{}), recvDone: make(chan struct{})}
		agentStreamCh <- fakeAgent
		require.NoError(t, rpc.NezhaHandlerSingleton.AgentConnected(req.StreamID, grpcx.NewIOStreamWrapper(fakeAgent)))
		close(attachReady)
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
	<-attachReady
	_, getErr := rpc.NezhaHandlerSingleton.GetStream(streamID)
	require.NoError(t, getErr, "stream must be live before cancel")
	agentStream := <-agentStreamCh
	readDone := make(chan error, 1)
	go func() {
		_, readErr := streamIO.Read(make([]byte, 16))
		readDone <- readErr
	}()

	cancel()

	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("cancelling the transfer context must unblock the actual streamIO.Read")
	}
	select {
	case <-agentStream.closeSeen:
	case <-time.After(time.Second):
		t.Fatal("cancelling the transfer context must close the fake agent endpoint")
	}
	select {
	case <-agentStream.closed:
	case <-time.After(time.Second):
		t.Fatal("cancelling the transfer context must close the handler endpoint")
	}
	select {
	case <-agentStream.recvDone:
	case <-time.After(time.Second):
		t.Fatal("cancelling the transfer context must let the fake handler exit")
	}
	require.Equal(t, int32(1), agentStream.closeCall.Load(), "attached endpoint must be closed exactly once")
	require.Equal(t, 0, rpc.NezhaHandlerSingleton.StreamCount())
	for index := 0; index < 40; index++ {
		require.NoError(t, rpc.NezhaHandlerSingleton.CreateStream(fmt.Sprintf("cancel-reuse-%d", index), 0, 7))
	}
	require.ErrorIs(t, rpc.NezhaHandlerSingleton.CreateStream("cancel-reuse-over", 0, 7), rpc.ErrTooManyStreamsForServer)
	for index := 0; index < 40; index++ {
		require.NoError(t, rpc.NezhaHandlerSingleton.CloseStream(fmt.Sprintf("cancel-reuse-%d", index)))
	}
	require.Equal(t, 0, rpc.NezhaHandlerSingleton.StreamCount())

	cleanupDone := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cleanup()
			}()
		}
		wg.Wait()
		close(cleanupDone)
	}()
	select {
	case <-cleanupDone:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent cleanup calls must complete")
	}
}
