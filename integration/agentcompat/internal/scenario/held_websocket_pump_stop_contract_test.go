//go:build linux

package scenario

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func TestHeldWebSocketPumpStopRetainsPeerAndCloseErrorsAfterCanceledWaiter(t *testing.T) {
	// Given
	closeErr := errors.New("physical close failed")
	server := heldPumpServer(t, func(connection *websocket.Conn) {
		require.NoError(t, connection.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "peer shutdown")))
	})
	recordedConnection := newPumpRecordingConn(closeErr)
	recordedConnection.closeEntered = make(chan struct{})
	recordedConnection.allowClose = make(chan struct{})
	t.Cleanup(func() { recordedConnection.releaseClose() })
	connection := heldPumpConnectionWithConn(t, server, recordedConnection)
	pump, err := newHeldWebSocketPump(context.Background(), connection, 1)
	require.NoError(t, err)
	require.NoError(t, pump.Wait(context.Background()))
	peerErr := pump.Err()
	require.Error(t, peerErr)
	stopContext, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	firstStopErr := pump.Stop(stopContext)
	recordedConnection.awaitClose(t)
	recordedConnection.releaseClose()
	laterStopErr := pump.Stop(context.Background())

	// Then
	require.ErrorIs(t, firstStopErr, context.Canceled)
	require.ErrorIs(t, laterStopErr, peerErr)
	require.ErrorIs(t, laterStopErr, closeErr)
	require.Equal(t, 1, recordedConnection.closeCount())
	require.Equal(t, laterStopErr, pump.Stop(context.Background()))
}

func TestHeldWebSocketPumpStopConcurrentCallersReceiveRetainedResult(t *testing.T) {
	// Given
	closeErr := errors.New("physical close failed")
	serverReady := make(chan struct{})
	server := heldPumpServer(t, func(connection *websocket.Conn) {
		close(serverReady)
		_, _, _ = connection.ReadMessage()
	})
	recordedConnection := newPumpRecordingConn(closeErr)
	connection := heldPumpConnectionWithConn(t, server, recordedConnection)
	pump, err := newHeldWebSocketPump(context.Background(), connection, 1)
	require.NoError(t, err)
	<-serverReady

	// When
	results := make(chan error, 8)
	for range cap(results) {
		go func() { results <- pump.Stop(context.Background()) }()
	}

	// Then
	var retainedResult error
	for range cap(results) {
		result := <-results
		require.ErrorIs(t, result, closeErr)
		if retainedResult == nil {
			retainedResult = result
			continue
		}
		require.Equal(t, retainedResult, result)
	}
	require.Equal(t, 1, recordedConnection.closeCount())
	require.Equal(t, retainedResult, pump.Stop(context.Background()))
	select {
	case <-pump.Done():
	default:
		t.Fatal("Stop returned before the reader joined")
	}
}

type pumpRecordingConn struct {
	net.Conn
	mu              sync.Mutex
	closeErr        error
	closeCountValue int
	closeEntered    chan struct{}
	allowClose      chan struct{}
	releaseOnce     sync.Once
}

func newPumpRecordingConn(closeErr error) *pumpRecordingConn {
	return &pumpRecordingConn{closeErr: closeErr}
}

func (connection *pumpRecordingConn) Close() error {
	connection.mu.Lock()
	connection.closeCountValue++
	connection.mu.Unlock()
	underlyingErr := connection.Conn.Close()
	if connection.closeEntered != nil {
		close(connection.closeEntered)
		<-connection.allowClose
	}
	if connection.closeErr != nil {
		return connection.closeErr
	}
	return underlyingErr
}

func (connection *pumpRecordingConn) awaitClose(t *testing.T) {
	t.Helper()
	select {
	case <-connection.closeEntered:
	case <-time.After(time.Second):
		t.Fatal("pump owner did not start physical close")
	}
}

func (connection *pumpRecordingConn) releaseClose() {
	if connection.allowClose != nil {
		connection.releaseOnce.Do(func() { close(connection.allowClose) })
	}
}

func (connection *pumpRecordingConn) closeCount() int {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return connection.closeCountValue
}

func heldPumpConnectionWithConn(t *testing.T, server *httptest.Server, recordedConnection *pumpRecordingConn) *client.WebSocketConnection {
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
	httpClient, err := client.New(client.Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, WebSocketDialer: &dialer})
	require.NoError(t, err)
	connection, err := httpClient.DialWebSocket(context.Background(), "/held")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}
