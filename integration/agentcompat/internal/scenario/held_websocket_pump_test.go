//go:build linux

package scenario

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func TestHeldWebSocketPumpPreservesFrameOrderAndType(t *testing.T) {
	serverReady := make(chan struct{})
	serverRelease := make(chan struct{})
	server := heldPumpServer(t, func(connection *websocket.Conn) {
		close(serverReady)
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("text")))
		require.NoError(t, connection.WriteMessage(websocket.BinaryMessage, []byte{1, 2}))
		<-serverRelease
	})
	connection := heldPumpConnection(t, server)
	pump, err := newHeldWebSocketPump(context.Background(), connection, 2)
	require.NoError(t, err)
	<-serverReady
	select {
	case first := <-pump.Events():
		require.Equal(t, client.FrameText, first.Type)
		require.Equal(t, []byte("text"), first.Payload)
	case <-time.After(time.Second):
		t.Fatal("missing text frame")
	}
	select {
	case second := <-pump.Events():
		require.Equal(t, client.FrameBinary, second.Type)
		require.Equal(t, []byte{1, 2}, second.Payload)
	case <-time.After(time.Second):
		t.Fatal("missing binary frame")
	}
	require.NoError(t, pump.Stop(context.Background()))
	close(serverRelease)
}

func TestHeldWebSocketPumpParentCancellationJoinsReader(t *testing.T) {
	serverReady := make(chan struct{})
	serverRelease := make(chan struct{})
	server := heldPumpServer(t, func(*websocket.Conn) {
		close(serverReady)
		<-serverRelease
	})
	connection := heldPumpConnection(t, server)
	parent, cancel := context.WithCancel(context.Background())
	pump, err := newHeldWebSocketPump(parent, connection, 1)
	require.NoError(t, err)
	<-serverReady
	cancel()
	require.NoError(t, pump.Wait(context.Background()))
	require.NoError(t, pump.Err())
	_, ok := <-pump.Events()
	require.False(t, ok)
	require.NoError(t, pump.Stop(context.Background()))
	require.NoError(t, pump.Stop(context.Background()))
	require.NoError(t, pump.Err())
	close(serverRelease)
}

func TestHeldWebSocketPumpStopJoinsBlockedRead(t *testing.T) {
	serverReady := make(chan struct{})
	serverRelease := make(chan struct{})
	server := heldPumpServer(t, func(*websocket.Conn) {
		close(serverReady)
		<-serverRelease
	})
	connection := heldPumpConnection(t, server)
	pump, err := newHeldWebSocketPump(context.Background(), connection, 1)
	require.NoError(t, err)
	<-serverReady
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pump.Stop(stopContext))
	require.NoError(t, pump.Wait(context.Background()))
	require.NoError(t, pump.Err())
	_, ok := <-pump.Events()
	require.False(t, ok)
	close(serverRelease)
}

func TestHeldWebSocketPumpBufferFullFailsFast(t *testing.T) {
	serverReady := make(chan struct{})
	server := heldPumpServer(t, func(connection *websocket.Conn) {
		close(serverReady)
		for _, payload := range []string{"one", "two"} {
			require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte(payload)))
		}
	})
	connection := heldPumpConnection(t, server)
	pump, err := newHeldWebSocketPump(context.Background(), connection, 1)
	require.NoError(t, err)
	<-serverReady
	select {
	case <-pump.Done():
	case <-time.After(time.Second):
		t.Fatal("buffer-full pump did not stop")
	}
	require.ErrorIs(t, pump.Err(), ErrHeldFrameBufferFull)
	require.NoError(t, pump.Wait(context.Background()))
	require.ErrorIs(t, pump.Stop(context.Background()), ErrHeldFrameBufferFull)
	require.ErrorIs(t, pump.Stop(context.Background()), ErrHeldFrameBufferFull)
}

func TestHeldWebSocketPumpStopIsIdempotentAndRetainsPeerError(t *testing.T) {
	server := heldPumpServer(t, func(connection *websocket.Conn) {
		require.NoError(t, connection.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "peer")))
	})
	connection := heldPumpConnection(t, server)
	pump, err := newHeldWebSocketPump(context.Background(), connection, 1)
	require.NoError(t, err)
	if err := pump.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	peerErr := pump.Err()
	require.NotNil(t, peerErr)
	require.ErrorIs(t, pump.Stop(context.Background()), peerErr)
	require.ErrorIs(t, pump.Stop(context.Background()), peerErr)
}

func heldPumpServer(t *testing.T, serve func(*websocket.Conn)) *httptest.Server {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		serve(connection)
	}))
	t.Cleanup(server.Close)
	return server
}

func heldPumpConnection(t *testing.T, server *httptest.Server) *client.WebSocketConnection {
	httpClient := newTestClient(t, server.URL)
	connection, err := httpClient.DialWebSocket(context.Background(), "/held")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func newTestClient(t *testing.T, baseURL string) *client.Client {
	result, err := client.New(client.Config{BaseURL: baseURL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	require.NoError(t, err)
	return result
}
