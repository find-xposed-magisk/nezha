package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestClient_ReadFrameUntil_UsesCallerDeadlineInsteadOfRequestTimeout(t *testing.T) {
	// Given
	upgrader := websocket.Upgrader{}
	serverReady := make(chan struct{})
	allowFrame := make(chan struct{})
	server := newWebSocketTestServer(t, upgrader, func(connection *websocket.Conn, _ *http.Request) {
		close(serverReady)
		<-allowFrame
		require.NoError(t, connection.WriteMessage(websocket.BinaryMessage, []byte("held-session")))
	})
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: 100 * time.Millisecond, MaxResponseBytes: 1024})
	connection, err := client.DialWebSocket(context.Background(), "/held")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	callerContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan struct {
		frame Frame
		err   error
	}, 1)

	// When
	go func() {
		frame, readErr := connection.ReadFrameUntil(callerContext)
		result <- struct {
			frame Frame
			err   error
		}{frame: frame, err: readErr}
	}()
	<-serverReady
	requestTimeout, cancelRequestTimeout := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelRequestTimeout()
	<-requestTimeout.Done()
	close(allowFrame)

	// Then
	select {
	case readResult := <-result:
		require.NoError(t, readResult.err)
		require.Equal(t, FrameBinary, readResult.frame.Type)
		require.Equal(t, []byte("held-session"), readResult.frame.Payload)
	case <-time.After(time.Second):
		t.Fatal("ReadFrameUntil did not receive the channel-released frame")
	}
}

func TestClient_ReadFrameUntil_ReturnsParentCancellationAndUnblocksRead(t *testing.T) {
	// Given
	upgrader := websocket.Upgrader{}
	serverReady := make(chan struct{})
	server := newWebSocketTestServer(t, upgrader, func(_ *websocket.Conn, request *http.Request) {
		close(serverReady)
		<-request.Context().Done()
	})
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	connection, err := client.DialWebSocket(context.Background(), "/cancel")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	callerContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)

	// When
	go func() {
		_, readErr := connection.ReadFrameUntil(callerContext)
		result <- readErr
	}()
	<-serverReady
	cancel()

	// Then
	select {
	case readErr := <-result:
		require.ErrorIs(t, readErr, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("ReadFrameUntil did not stop after parent cancellation")
	}
}

func newWebSocketTestServer(t *testing.T, upgrader websocket.Upgrader, serve func(*websocket.Conn, *http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		serve(connection, request)
	}))
	t.Cleanup(server.Close)
	return server
}
