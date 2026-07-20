package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClient_TransferURLConsumptionOmitsAuthorization(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/mcp":
			require.Equal(t, "Bearer mcp-token", request.Header.Get("Authorization"))
			var rpcRequest testJSONRPCRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&rpcRequest))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":` + jsonNumber(rpcRequest.ID) + `,"result":{"structuredContent":{"url":"` + server.URL + `/mcp/download/one-time-token","method":"GET","expires_at":"2030-01-02T03:04:05Z"}}}`))
		case "/mcp/download/one-time-token":
			require.Empty(t, request.Header.Get("Authorization"))
			require.Empty(t, request.Header.Get("Origin"))
			_, _ = writer.Write([]byte("transfer payload"))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		BearerToken:      "mcp-token",
		Origin:           server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
		MaxTransferBytes: 1024,
	})
	transfer, err := RequestDownloadURL(context.Background(), client, DownloadURLRequest{ServerID: 7, Path: "/tmp/report", TTLSeconds: 30})
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, transfer.Method)

	var destination bytes.Buffer
	written, err := client.DownloadTransfer(context.Background(), transfer, &destination)
	require.NoError(t, err)
	require.Equal(t, int64(len("transfer payload")), written)
	require.Equal(t, "transfer payload", destination.String())
}

func TestClient_TransferURLRejectsCrossOrigin(t *testing.T) {
	foreignServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cross-origin transfer request was dispatched")
	}))
	t.Cleanup(foreignServer.Close)

	client := newTestClient(t, Config{
		BaseURL:          "http://dashboard.example",
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
		MaxTransferBytes: 1024,
	})

	var destination bytes.Buffer
	_, err := client.DownloadTransfer(context.Background(), TransferURL{URL: foreignServer.URL + "/mcp/download/token", Method: http.MethodGet}, &destination)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidConfig))
}

func TestClient_TransferURLRejectsCrossOriginRedirect(t *testing.T) {
	var foreignRequests atomic.Int32
	foreignServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		foreignRequests.Add(1)
	}))
	t.Cleanup(foreignServer.Close)

	dashboardServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Redirect(writer, &http.Request{}, foreignServer.URL+"/stolen-token", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(dashboardServer.Close)

	client := newTestClient(t, Config{
		BaseURL:          dashboardServer.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
		MaxTransferBytes: 1024,
	})

	var destination bytes.Buffer
	_, err := client.DownloadTransfer(context.Background(), TransferURL{
		URL:       dashboardServer.URL + "/mcp/download/token",
		Method:    http.MethodGet,
		ExpiresAt: time.Now().Add(time.Minute),
	}, &destination)
	require.ErrorIs(t, err, ErrRedirect)
	require.Zero(t, foreignRequests.Load())
}

func TestClient_TransferURLRejectsSameOriginRedirect(t *testing.T) {
	var redirected atomic.Int32
	var dashboardServer *httptest.Server
	dashboardServer = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/capture" {
			redirected.Add(1)
			return
		}
		http.Redirect(writer, request, dashboardServer.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(dashboardServer.Close)

	client := newTestClient(t, Config{BaseURL: dashboardServer.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, MaxTransferBytes: 1024})
	var destination bytes.Buffer
	_, err := client.DownloadTransfer(context.Background(), TransferURL{
		URL:       dashboardServer.URL + "/mcp/download/token",
		Method:    http.MethodGet,
		ExpiresAt: time.Now().Add(time.Minute),
	}, &destination)
	require.ErrorIs(t, err, ErrRedirect)
	require.Zero(t, redirected.Load())
}

func TestClient_TransferURLRejectsExpiredCapability(t *testing.T) {
	client := newTestClient(t, Config{BaseURL: "http://dashboard.example", RequestTimeout: time.Second, MaxResponseBytes: 1024, MaxTransferBytes: 1024})
	var destination bytes.Buffer
	_, err := client.DownloadTransfer(context.Background(), TransferURL{
		URL:       "http://dashboard.example/mcp/download/token",
		Method:    http.MethodGet,
		ExpiresAt: time.Now().Add(-time.Second),
	}, &destination)
	require.ErrorIs(t, err, ErrTransferExpired)
}

func TestClient_TransferURLRejectsMissingExpiryBeforeDispatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, MaxTransferBytes: 1024})
	var destination bytes.Buffer
	_, err := client.DownloadTransfer(context.Background(), TransferURL{
		URL:    server.URL + "/mcp/download/token",
		Method: http.MethodGet,
	}, &destination)
	require.ErrorIs(t, err, ErrTransferExpired)
	require.Zero(t, requests.Load())
}

func TestClient_RequestUploadURLRejectsMissingExpiryBeforeDispatch(t *testing.T) {
	var requests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"url":"` + server.URL + `/mcp/upload/token","method":"POST"}}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, MaxTransferBytes: 1024})
	_, err := RequestUploadURL(context.Background(), client, UploadURLRequest{ServerID: 7, Path: "/tmp/report"})
	require.ErrorIs(t, err, ErrTransferExpired)
	require.Equal(t, int32(1), requests.Load())
}

func TestClient_TransferRejectsOversizeWithoutWritingPastLimit(t *testing.T) {
	dashboardServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 65)))
	}))
	t.Cleanup(dashboardServer.Close)

	client := newTestClient(t, Config{
		BaseURL:          dashboardServer.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
		MaxTransferBytes: 64,
	})

	var destination bytes.Buffer
	written, err := client.DownloadTransfer(context.Background(), TransferURL{
		URL:       dashboardServer.URL + "/mcp/download/token",
		Method:    http.MethodGet,
		ExpiresAt: time.Now().Add(time.Minute),
	}, &destination)
	require.ErrorIs(t, err, ErrTransferTooLarge)
	require.Equal(t, int64(64), written)
	require.Len(t, destination.Bytes(), 64)
}

func TestClient_UploadRejectsChunkedBodyWithoutDispatch(t *testing.T) {
	var requests atomic.Int32
	dashboardServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(dashboardServer.Close)

	client := newTestClient(t, Config{
		BaseURL:          dashboardServer.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
		MaxTransferBytes: 64,
	})
	_, err := client.UploadTransfer(context.Background(), TransferURL{
		URL:       dashboardServer.URL + "/mcp/upload/token",
		Method:    http.MethodPost,
		ExpiresAt: time.Now().Add(time.Minute),
	}, UploadTransfer{Body: strings.NewReader("hidden body"), ContentLength: 0})
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Zero(t, requests.Load())
}

func TestClient_UploadRejectsMisleadingSuccessEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"data":{"size":11,"sha256":"abc"}}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, MaxTransferBytes: 1024})
	_, err := client.UploadTransfer(context.Background(), TransferURL{
		URL:       server.URL + "/mcp/upload/token",
		Method:    http.MethodPost,
		ExpiresAt: time.Now().Add(time.Minute),
	}, UploadTransfer{Body: strings.NewReader("payload"), ContentLength: int64(len("payload"))})
	require.ErrorIs(t, err, ErrSemanticFailure)
}

func TestClient_UploadRejectsMissingRequiredResultFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"size":7}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024, MaxTransferBytes: 1024})
	_, err := client.UploadTransfer(context.Background(), TransferURL{
		URL:       server.URL + "/mcp/upload/token",
		Method:    http.MethodPost,
		ExpiresAt: time.Now().Add(time.Minute),
	}, UploadTransfer{Body: strings.NewReader("payload"), ContentLength: int64(len("payload"))})
	require.ErrorIs(t, err, ErrSemanticFailure)
}
