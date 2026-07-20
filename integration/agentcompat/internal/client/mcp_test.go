package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type testToolCallParams struct {
	Name string `json:"name"`
}

type fileReadArguments struct {
	ServerID uint64 `json:"server_id"`
	Path     string `json:"path"`
}

type fileReadResult struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func TestClient_MCPStructuredContent(t *testing.T) {
	requestIDs := make(chan uint64, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/mcp", request.URL.Path)
		require.Equal(t, "Bearer mcp-token", request.Header.Get("Authorization"))
		require.NotEmpty(t, request.Header.Get("Origin"))

		var rpcRequest testJSONRPCRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&rpcRequest))
		require.Equal(t, "2.0", rpcRequest.JSONRPC)
		require.Equal(t, "tools/call", rpcRequest.Method)
		var params testToolCallParams
		require.NoError(t, json.Unmarshal(rpcRequest.Params, &params))
		require.Equal(t, "fs.read", params.Name)
		requestIDs <- rpcRequest.ID

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":` + jsonNumber(rpcRequest.ID) + `,"result":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"size":7,"sha256":"abc123"}}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		BearerToken:      "mcp-token",
		Origin:           server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
	})
	call := ToolCall[fileReadArguments]{Name: "fs.read", Arguments: fileReadArguments{ServerID: 7, Path: "/tmp/report"}}
	first, err := CallTool[fileReadArguments, fileReadResult](context.Background(), client, call)
	require.NoError(t, err)
	require.Equal(t, int64(7), first.StructuredContent.Size)
	require.Equal(t, "abc123", first.StructuredContent.SHA256)

	_, err = CallTool[fileReadArguments, fileReadResult](context.Background(), client, call)
	require.NoError(t, err)
	require.Equal(t, uint64(1), <-requestIDs)
	require.Equal(t, uint64(2), <-requestIDs)
}

func TestClient_MCPUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"unauthorized"}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		BearerToken:      "invalid-token",
		Origin:           server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
	})
	_, err := CallTool[fileReadArguments, fileReadResult](context.Background(), client, ToolCall[fileReadArguments]{
		Name:      "fs.read",
		Arguments: fileReadArguments{ServerID: 7, Path: "/tmp/report"},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnauthorized))
}

func TestClient_MCPClassifiesNonJSONUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte("unauthorized"))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	_, err := client.Initialize(context.Background())
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestClient_MCPToolSemanticFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testJSONRPCRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&rpcRequest))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":` + jsonNumber(rpcRequest.ID) + `,"result":{"content":[{"type":"text","text":"agent offline"}],"isError":true}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	_, err := CallTool[fileReadArguments, fileReadResult](context.Background(), client, ToolCall[fileReadArguments]{
		Name:      "fs.read",
		Arguments: fileReadArguments{ServerID: 7, Path: "/tmp/report"},
	})
	require.ErrorIs(t, err, ErrToolFailure)
}

func TestClient_MCPToolFailurePreservesStructuredContent(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testJSONRPCRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&rpcRequest))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":` + jsonNumber(rpcRequest.ID) + `,"result":{"content":[{"type":"text","text":"command not found"}],"structuredContent":{"exit_code":127,"error":"command or working directory not found"},"isError":true}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	_, err := CallTool[fileReadArguments, fileReadResult](context.Background(), client, ToolCall[fileReadArguments]{Name: "server.exec", Arguments: fileReadArguments{ServerID: 7}})

	// Then
	var toolFailure *ToolFailure
	require.ErrorAs(t, err, &toolFailure)
	require.ErrorIs(t, err, ErrToolFailure)
	require.Equal(t, "command not found", toolFailure.Message)
	require.JSONEq(t, `{"exit_code":127,"error":"command or working directory not found"}`, string(toolFailure.StructuredContent))
}

func TestClient_MCPArbitraryTransportErrorIsNotToolFailure(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: 20 * time.Millisecond, MaxResponseBytes: 1024})

	// When
	_, err := CallTool[fileReadArguments, fileReadResult](context.Background(), client, ToolCall[fileReadArguments]{Name: "server.exec", Arguments: fileReadArguments{ServerID: 7}})

	// Then
	var toolFailure *ToolFailure
	require.Error(t, err)
	require.NotErrorAs(t, err, &toolFailure)
	require.NotErrorIs(t, err, ErrToolFailure)
}

func TestClient_MCPMalformedStructuredToolFailureIsTyped(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testJSONRPCRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&rpcRequest))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":` + jsonNumber(rpcRequest.ID) + `,"result":{"content":[{"type":"text","text":"invalid command"}],"structuredContent":"not-an-object","isError":true}}`))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})

	// When
	_, err := CallTool[fileReadArguments, fileReadResult](context.Background(), client, ToolCall[fileReadArguments]{Name: "server.exec", Arguments: fileReadArguments{ServerID: 7}})

	// Then
	var toolFailure *ToolFailure
	require.ErrorAs(t, err, &toolFailure)
	require.ErrorIs(t, err, ErrToolFailure)
	require.JSONEq(t, `"not-an-object"`, string(toolFailure.StructuredContent))
}

func TestClient_MCPRejectsOversize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"padding":"` + string(make([]byte, 256)) + `"}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 64})
	_, err := client.Initialize(context.Background())
	require.ErrorIs(t, err, ErrResponseTooLarge)
}

func TestClient_MCPDeadline(t *testing.T) {
	// The handler must enter before the client deadline: scheduling the handler
	// against a tiny timeout can make this test miss a real MCP request.
	requestBodyDrained := make(chan struct{})
	requestCancelled := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			return
		}
		close(requestBodyDrained)
		select {
		case <-request.Context().Done():
			close(requestCancelled)
		case <-releaseHandler:
		}
	}))
	t.Cleanup(func() {
		close(releaseHandler)
		server.Close()
	})

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	result := make(chan error, 1)
	go func() {
		_, err := client.Initialize(context.Background())
		result <- err
	}()
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-requestBodyDrained:
	case <-waitContext.Done():
		t.Fatal("server did not receive MCP request")
	}
	var err error
	select {
	case err = <-result:
	case <-waitContext.Done():
		t.Fatal("MCP request did not reach its deadline")
	}
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-requestCancelled:
	case <-waitContext.Done():
		t.Fatal("server request context was not cancelled")
	}
}

func TestClient_MCPRejectsResultAndErrorTogether(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testJSONRPCRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&rpcRequest))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":` + jsonNumber(rpcRequest.ID) + `,"result":{},"error":{"code":-32603,"message":"invalid"}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	_, err := client.Initialize(context.Background())
	require.ErrorIs(t, err, ErrJSONRPC)
}

func TestClient_MCPRejectsRedirectWithoutForwardingAuthorization(t *testing.T) {
	var redirected atomic.Int32
	foreignServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	t.Cleanup(foreignServer.Close)

	dashboardServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Redirect(writer, &http.Request{}, foreignServer.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(dashboardServer.Close)

	client := newTestClient(t, Config{BaseURL: dashboardServer.URL, BearerToken: "mcp-token", RequestTimeout: time.Second, MaxResponseBytes: 1024})
	_, err := client.Initialize(context.Background())
	require.ErrorIs(t, err, ErrRedirect)
	require.Zero(t, redirected.Load())
}

func jsonNumber(value uint64) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
