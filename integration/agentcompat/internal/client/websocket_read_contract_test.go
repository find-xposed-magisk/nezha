package client

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestClient_ReadFrameUntil_ClearsReadDeadlineOnUnderlyingConnection(t *testing.T) {
	// Given
	firstFrame := make(chan struct{})
	allowSecondFrame := make(chan struct{})
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(connection *websocket.Conn, _ *http.Request) {
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("first")))
		close(firstFrame)
		<-allowSecondFrame
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("second")))
	})
	recordedConnection := newRecordingDeadlineConn()
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, WebSocketDialer: recordingWebSocketDialer(recordedConnection)})
	connection, err := client.DialWebSocket(context.Background(), "/deadline-clear")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	shortContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	<-firstFrame
	_, err = connection.ReadFrame(shortContext)
	require.NoError(t, err)
	firstDeadline := recordedConnection.awaitReadDeadline(t)
	require.False(t, firstDeadline.IsZero())

	// When
	result := make(chan error, 1)
	go func() {
		_, readErr := connection.ReadFrameUntil(context.Background())
		result <- readErr
	}()
	require.True(t, recordedConnection.awaitReadDeadline(t).IsZero())
	close(allowSecondFrame)

	// Then
	require.NoError(t, <-result)
}

func TestClient_ReadFrameUntil_UsesCallerDeadlineInsteadOfDefaultTimeout(t *testing.T) {
	// Given
	allowFrame := make(chan struct{})
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(connection *websocket.Conn, _ *http.Request) {
		<-allowFrame
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("held")))
	})
	recordedConnection := newRecordingDeadlineConn()
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, WebSocketDialer: recordingWebSocketDialer(recordedConnection)})
	connection, err := client.DialWebSocket(context.Background(), "/caller-deadline")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	wantDeadline := time.Now().Add(time.Hour)
	callerContext, cancel := context.WithDeadline(context.Background(), wantDeadline)
	defer cancel()
	result := make(chan error, 1)

	// When
	go func() {
		_, readErr := connection.ReadFrameUntil(callerContext)
		result <- readErr
	}()

	// Then
	require.Equal(t, wantDeadline, recordedConnection.awaitReadDeadline(t))
	close(allowFrame)
	require.NoError(t, <-result)
}

func TestClient_ReadFrameUntil_ReturnsCancellationWhenItWinsAfterRead(t *testing.T) {
	// Given
	frameSent := make(chan struct{})
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(connection *websocket.Conn, _ *http.Request) {
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("first")))
		close(frameSent)
		_, _, _ = connection.ReadMessage()
	})
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	connection, err := client.DialWebSocket(context.Background(), "/cancel-wins")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	callerContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	connection.afterReadMessageForTest = cancel
	<-frameSent

	// When
	_, err = connection.ReadFrameUntil(callerContext)

	// Then
	require.ErrorIs(t, err, context.Canceled)
}

func TestClient_ReadFrameUntil_ReturnsCancellationWhenDeadlineSetIsInterrupted(t *testing.T) {
	// Given
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(_ *websocket.Conn, request *http.Request) {
		<-request.Context().Done()
	})
	recordedConnection := newRecordingDeadlineConn()
	recordedConnection.readDeadlineEntered = make(chan struct{}, 1)
	recordedConnection.allowReadDeadline = make(chan struct{})
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, WebSocketDialer: recordingWebSocketDialer(recordedConnection)})
	connection, err := client.DialWebSocket(context.Background(), "/deadline-cancel")
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
	recordedConnection.awaitReadDeadlineEntered(t)
	cancel()
	recordedConnection.awaitClose(t)
	close(recordedConnection.allowReadDeadline)

	// Then
	require.ErrorIs(t, <-result, context.Canceled)
}

func TestClient_ReadFrameUntil_KeepsConnectionOpenWhenCanceledAfterSuccess(t *testing.T) {
	// Given
	firstFrameSent := make(chan struct{})
	allowSecondFrame := make(chan struct{})
	server := newWebSocketTestServer(t, websocket.Upgrader{}, func(connection *websocket.Conn, _ *http.Request) {
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("first")))
		close(firstFrameSent)
		<-allowSecondFrame
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte("second")))
	})
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	connection, err := client.DialWebSocket(context.Background(), "/success-cancel")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	callerContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	<-firstFrameSent

	// When
	firstFrame, err := connection.ReadFrameUntil(callerContext)
	require.NoError(t, err)
	cancel()
	close(allowSecondFrame)
	secondFrame, err := connection.ReadFrameUntil(context.Background())

	// Then
	require.Equal(t, []byte("first"), firstFrame.Payload)
	require.NoError(t, err)
	require.Equal(t, []byte("second"), secondFrame.Payload)
}

type recordingDeadlineConn struct {
	net.Conn
	mu                  sync.Mutex
	readDeadlines       []time.Time
	readDeadlineCalls   chan time.Time
	readDeadlineEntered chan struct{}
	allowReadDeadline   chan struct{}
	closeCalls          chan struct{}
}

func (connection *recordingDeadlineConn) SetReadDeadline(deadline time.Time) error {
	connection.mu.Lock()
	connection.readDeadlines = append(connection.readDeadlines, deadline)
	connection.mu.Unlock()
	connection.readDeadlineCalls <- deadline
	if connection.readDeadlineEntered != nil {
		connection.readDeadlineEntered <- struct{}{}
		<-connection.allowReadDeadline
	}
	return connection.Conn.SetReadDeadline(deadline)
}

func (connection *recordingDeadlineConn) Close() error {
	connection.closeCalls <- struct{}{}
	return connection.Conn.Close()
}

func newRecordingDeadlineConn() *recordingDeadlineConn {
	return &recordingDeadlineConn{readDeadlineCalls: make(chan time.Time, 4), closeCalls: make(chan struct{}, 2)}
}

func (connection *recordingDeadlineConn) awaitReadDeadline(t *testing.T) time.Time {
	t.Helper()
	select {
	case deadline := <-connection.readDeadlineCalls:
		return deadline
	case <-time.After(time.Second):
		t.Fatal("WebSocket client did not set a read deadline")
		return time.Time{}
	}
}

func (connection *recordingDeadlineConn) awaitReadDeadlineEntered(t *testing.T) {
	t.Helper()
	select {
	case <-connection.readDeadlineEntered:
	case <-time.After(time.Second):
		t.Fatal("WebSocket client did not enter SetReadDeadline")
	}
}

func (connection *recordingDeadlineConn) awaitClose(t *testing.T) {
	t.Helper()
	select {
	case <-connection.closeCalls:
	case <-time.After(time.Second):
		t.Fatal("WebSocket client did not close after cancellation")
	}
}

func recordingWebSocketDialer(recordedConnection *recordingDeadlineConn) *websocket.Dialer {
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		recordedConnection.Conn = connection
		return recordedConnection, nil
	}
	return &dialer
}
