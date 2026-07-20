package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

type failingRequestTaskStream struct {
	pb.NezhaService_RequestTaskServer
	mu        sync.Mutex
	sendCalls int
	err       error
}

func (stream *failingRequestTaskStream) Send(*pb.Task) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.sendCalls++
	return stream.err
}

func (stream *failingRequestTaskStream) calls() int {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.sendCalls
}

func (stream *failingRequestTaskStream) Context() context.Context     { return context.Background() }
func (stream *failingRequestTaskStream) SetHeader(metadata.MD) error  { return nil }
func (stream *failingRequestTaskStream) SendHeader(metadata.MD) error { return nil }
func (stream *failingRequestTaskStream) SetTrailer(metadata.MD)       {}
func (stream *failingRequestTaskStream) SendMsg(any) error            { return nil }
func (stream *failingRequestTaskStream) RecvMsg(any) error            { return nil }

func newAuthorizedControllerContext(t *testing.T, method, target string, body any) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	context.Request = httptest.NewRequest(method, target, bytes.NewReader(encoded))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set(model.CtxKeyAuthorizedUser, &model.User{Common: model.Common{ID: 100}, Role: model.RoleMember})
	return context
}

func TestCreateTerminalReturnsSendErrorAndReleasesStreamCapacity(t *testing.T) {
	cleanupFixture, _ := setupMCPTest(t)
	defer cleanupFixture()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })

	sendError := errors.New("terminal task send failed")
	stream := &failingRequestTaskStream{err: sendError}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(stream)

	request := newAuthorizedControllerContext(t, "POST", "/terminal", model.TerminalForm{ServerID: 7})
	response, err := createTerminal(request)
	require.ErrorIs(t, err, sendError)
	require.Nil(t, response)
	require.Equal(t, 1, stream.calls())
	assertStreamCapacityReusable(t, rpc.NezhaHandlerSingleton, 100, 7, "terminal-reused")
}

func TestCreateFMReturnsSendErrorAndReleasesStreamCapacity(t *testing.T) {
	cleanupFixture, _ := setupMCPTest(t)
	defer cleanupFixture()
	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()
	t.Cleanup(func() { rpc.NezhaHandlerSingleton = originalHandler })

	sendError := errors.New("FM task send failed")
	stream := &failingRequestTaskStream{err: sendError}
	server, ok := singleton.ServerShared.Get(7)
	require.True(t, ok)
	server.SetTaskStream(stream)

	request := newAuthorizedControllerContext(t, "POST", "/file?id=7", nil)
	response, err := createFM(request)
	require.ErrorIs(t, err, sendError)
	require.Nil(t, response)
	require.Equal(t, 1, stream.calls())
	assertStreamCapacityReusable(t, rpc.NezhaHandlerSingleton, 100, 7, "fm-reused")
}

func assertStreamCapacityReusable(t *testing.T, handler *rpc.NezhaHandler, userID, serverID uint64, streamID string) {
	t.Helper()
	_, tracked := handler.StreamOwnership(streamID)
	require.False(t, tracked, "failed task dispatch must not leave the replacement stream tracked")
	for index := 0; index < 20; index++ {
		require.NoError(t, handler.CreateStream(streamID+"-user-"+ctoa(uint64(index)), userID, serverID+uint64(index)))
	}
	require.ErrorIs(t, handler.CreateStream(streamID+"-user-over", userID, serverID+100), rpc.ErrTooManyStreamsForUser)
	for index := 0; index < 20; index++ {
		require.NoError(t, handler.CloseStream(streamID+"-user-"+ctoa(uint64(index))))
	}
	for index := 0; index < 40; index++ {
		require.NoError(t, handler.CreateStream(streamID+"-server-"+ctoa(uint64(index)), userID+uint64(index)+1000, serverID+1000))
	}
	require.ErrorIs(t, handler.CreateStream(streamID+"-server-over", userID+1000, serverID+1000), rpc.ErrTooManyStreamsForServer)
	for index := 0; index < 40; index++ {
		require.NoError(t, handler.CloseStream(streamID+"-server-"+ctoa(uint64(index))))
	}
}
