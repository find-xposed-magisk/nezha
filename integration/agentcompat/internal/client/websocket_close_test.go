package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestClient_WebSocketClose_RetainsConcurrentPhysicalCloseResult(t *testing.T) {
	// Given
	closeErr := errors.New("physical close failed")
	recordedConnection := newRetainedCloseConn(closeErr)
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(_ *websocket.Conn, request *http.Request) {
		<-request.Context().Done()
	})
	connection := dialRetainedCloseWebSocket(t, server.URL, recordedConnection)

	// When
	results := make(chan error, 8)
	for range cap(results) {
		go func() { results <- connection.Close() }()
	}

	// Then
	for range cap(results) {
		require.Equal(t, closeErr, <-results)
	}
	require.Equal(t, 1, recordedConnection.closeCount())
	require.Equal(t, closeErr, connection.Close())
}

func TestClient_WebSocketClose_RetainsReadCancellationCloseResult(t *testing.T) {
	// Given
	closeErr := errors.New("read cancellation close failed")
	recordedConnection := newRetainedCloseConn(closeErr)
	serverReady := make(chan struct{})
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(_ *websocket.Conn, request *http.Request) {
		close(serverReady)
		<-request.Context().Done()
	})
	connection := dialRetainedCloseWebSocket(t, server.URL, recordedConnection)
	readContext, cancel := context.WithCancel(context.Background())
	readResult := make(chan error, 1)
	go func() {
		_, readErr := connection.ReadFrameUntil(readContext)
		readResult <- readErr
	}()
	<-serverReady

	// When
	cancel()

	// Then
	require.ErrorIs(t, <-readResult, context.Canceled)
	require.Equal(t, closeErr, connection.Close())
	require.Equal(t, 1, recordedConnection.closeCount())
}

func TestClient_WebSocketClose_RetainsWriteCancellationCloseResult(t *testing.T) {
	// Given
	closeErr := errors.New("write cancellation close failed")
	recordedConnection := newRetainedCloseConn(closeErr)
	serverReady := make(chan struct{})
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(_ *websocket.Conn, request *http.Request) {
		close(serverReady)
		<-request.Context().Done()
	})
	connection := dialRetainedCloseWebSocket(t, server.URL, recordedConnection)
	<-serverReady
	recordedConnection.blockWrites()
	writeContext, cancel := context.WithCancel(context.Background())
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- connection.WriteFrame(writeContext, Frame{Type: FrameBinary, Payload: []byte("blocked")})
	}()
	recordedConnection.awaitWrite(t)

	// When
	cancel()

	// Then
	require.ErrorIs(t, <-writeResult, context.Canceled)
	require.Equal(t, closeErr, connection.Close())
	require.Equal(t, 1, recordedConnection.closeCount())
}

type retainedCloseConn struct {
	net.Conn
	mu              sync.Mutex
	closeErr        error
	closeCountValue int
	writeBlocked    bool
	writeEntered    chan struct{}
	writeReleased   chan struct{}
	releaseWrite    sync.Once
}

func newRetainedCloseConn(closeErr error) *retainedCloseConn {
	return &retainedCloseConn{closeErr: closeErr, writeEntered: make(chan struct{}, 1), writeReleased: make(chan struct{})}
}

func (connection *retainedCloseConn) Write(payload []byte) (int, error) {
	connection.mu.Lock()
	blocked := connection.writeBlocked
	connection.mu.Unlock()
	if blocked {
		connection.writeEntered <- struct{}{}
		<-connection.writeReleased
	}
	return connection.Conn.Write(payload)
}

func (connection *retainedCloseConn) Close() error {
	connection.mu.Lock()
	connection.closeCountValue++
	connection.mu.Unlock()
	underlyingErr := connection.Conn.Close()
	connection.releaseWrite.Do(func() { close(connection.writeReleased) })
	if connection.closeErr != nil {
		return connection.closeErr
	}
	return underlyingErr
}

func (connection *retainedCloseConn) blockWrites() {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	connection.writeBlocked = true
}

func (connection *retainedCloseConn) awaitWrite(t *testing.T) {
	t.Helper()
	select {
	case <-connection.writeEntered:
	case <-time.After(time.Second):
		t.Fatal("WebSocket client did not enter the blocked write")
	}
}

func (connection *retainedCloseConn) closeCount() int {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.closeCountValue
}

func dialRetainedCloseWebSocket(t *testing.T, baseURL string, recordedConnection *retainedCloseConn) *WebSocketConnection {
	t.Helper()
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		recordedConnection.Conn = connection
		return recordedConnection, nil
	}
	client := newTestClient(t, Config{BaseURL: baseURL, RequestTimeout: time.Second, MaxResponseBytes: 1024, WebSocketDialer: &dialer})
	connection, err := client.DialWebSocket(context.Background(), "/retained-close")
	require.NoError(t, err)
	t.Cleanup(func() { require.ErrorIs(t, connection.Close(), recordedConnection.closeErr) })
	return connection
}
