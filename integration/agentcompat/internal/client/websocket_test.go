package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestClient_WebSocketFrameReassembly(t *testing.T) {
	upgrader := websocket.Upgrader{WriteBufferSize: 4}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "Bearer ws-token", request.Header.Get("Authorization"))
		require.NotEmpty(t, request.Header.Get("Origin"))
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()

		textWriter, err := connection.NextWriter(websocket.TextMessage)
		require.NoError(t, err)
		_, err = textWriter.Write([]byte("hello "))
		require.NoError(t, err)
		_, err = textWriter.Write([]byte("world"))
		require.NoError(t, err)
		require.NoError(t, textWriter.Close())

		binaryWriter, err := connection.NextWriter(websocket.BinaryMessage)
		require.NoError(t, err)
		_, err = binaryWriter.Write([]byte{1, 2})
		require.NoError(t, err)
		_, err = binaryWriter.Write([]byte{3, 4})
		require.NoError(t, err)
		require.NoError(t, binaryWriter.Close())
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		BearerToken:      "ws-token",
		Origin:           server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
	})
	connection, err := client.DialWebSocket(context.Background(), "/stream")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	textFrame, err := connection.ReadFrame(context.Background())
	require.NoError(t, err)
	require.Equal(t, FrameText, textFrame.Type)
	require.Equal(t, []byte("hello world"), textFrame.Payload)

	binaryFrame, err := connection.ReadFrame(context.Background())
	require.NoError(t, err)
	require.Equal(t, FrameBinary, binaryFrame.Type)
	require.Equal(t, []byte{1, 2, 3, 4}, binaryFrame.Payload)
}

func TestClient_WebSocketRejectsOversize(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		require.NoError(t, connection.WriteMessage(websocket.BinaryMessage, []byte(strings.Repeat("x", 65))))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 64,
	})
	connection, err := client.DialWebSocket(context.Background(), "/oversize")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	_, err = connection.ReadFrame(context.Background())
	require.ErrorIs(t, err, ErrResponseTooLarge)
}

func TestClient_DialWebSocket_ReturnsTypedHandshakeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "permission denied", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	webSocketClient := newTestClient(t, Config{BaseURL: server.URL})

	_, err := webSocketClient.DialWebSocket(context.Background(), "/terminal")

	var handshakeError *WebSocketHandshakeError
	require.ErrorAs(t, err, &handshakeError)
	require.Equal(t, http.StatusForbidden, handshakeError.StatusCode)
	require.Contains(t, handshakeError.Message, "permission denied")
}

func TestClient_WebSocketDeadline(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		_, _, _ = connection.ReadMessage()
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
	})
	connection, err := client.DialWebSocket(context.Background(), "/deadline")
	require.NoError(t, err)
	connection.timeout = 20 * time.Millisecond

	_, err = connection.ReadFrame(context.Background())
	require.True(t, errors.Is(err, context.DeadlineExceeded), err)
	require.NoError(t, connection.Close())
}

func TestClient_RedactsAuthorization(t *testing.T) {
	secret := "nzp_super-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":false,"error":"Authorization: Bearer ` + secret + `"}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		BearerToken:      secret,
		Origin:           server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
	})
	_, err := REST[struct{}, semanticResult](context.Background(), client, RESTRequest[struct{}]{Method: http.MethodGet, Path: "/redaction"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
	require.Contains(t, err.Error(), "[REDACTED]")

	redacted := Redact("request failed with Authorization: Bearer " + secret)
	require.False(t, strings.Contains(redacted, secret))
	require.Contains(t, redacted, "Authorization: Bearer [REDACTED]")

	quoted := Redact(`{"Authorization":"Bearer ` + secret + `"}`)
	require.NotContains(t, quoted, secret)
	require.Contains(t, quoted, "[REDACTED]")

	query := Redact("https://dashboard.example/mcp/download/path-token?access_token=" + secret + "&X-Amz-Signature=signature-secret")
	require.NotContains(t, query, secret)
	require.NotContains(t, query, "signature-secret")

	httpError := &HTTPError{StatusCode: http.StatusBadRequest, Message: Redact("Authorization: Bearer " + secret)}
	require.NotContains(t, httpError.Message, secret)
	rpcError := &RPCError{Code: -32603, Message: Redact("token=" + secret)}
	require.NotContains(t, rpcError.Message, secret)
}

func TestClient_RedactsCredentialClasses(t *testing.T) {
	secret := "sensitive-value"
	inputs := []string{
		"X-CSRF-Token: " + secret,
		"password=" + secret,
		"https://dashboard.example/path?access_token=" + secret,
		"jwt_token: " + secret,
	}
	for _, input := range inputs {
		redacted := Redact(input)
		require.NotContains(t, redacted, secret)
		require.Contains(t, redacted, "[REDACTED]")
	}
}

func TestClient_WebSocketReadDeadline(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: 20 * time.Millisecond, MaxResponseBytes: 1024})
	connection, err := client.DialWebSocket(context.Background(), "/stream")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	_, err = connection.ReadFrame(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestClient_WebSocketWriteHonorsParentCancellation(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverReady := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		close(serverReady)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: 5 * time.Second, MaxResponseBytes: 1024})
	connection, err := client.DialWebSocket(context.Background(), "/blocked-write")
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	writeContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	<-serverReady
	go func() {
		result <- connection.WriteFrame(writeContext, Frame{Type: FrameBinary, Payload: make([]byte, 128<<20)})
	}()
	cancel()
	select {
	case err = <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("WebSocket write did not stop after parent cancellation")
	}
}
