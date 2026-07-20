package client

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type semanticRequest struct {
	Name string `json:"name"`
}

type semanticResult struct {
	ID uint64 `json:"id"`
}

func TestClient_RESTSemanticSuccess(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "Bearer test-token", request.Header.Get("Authorization"))
		require.Equal(t, "csrf-value", request.Header.Get("X-CSRF-Token"))
		require.NotEmpty(t, request.Header.Get("Origin"))
		writer.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = writer.Write([]byte(`{"success":false,"error":"semantic failure"}`))
			return
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"success":true,"data":{"id":42}}`))
	}))
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	baseURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	jar.SetCookies(baseURL, []*http.Cookie{{Name: "nz-csrf", Value: "csrf-value"}})
	httpClient := server.Client()
	httpClient.Jar = jar
	client := newTestClient(t, Config{
		BaseURL:          server.URL,
		HTTPClient:       httpClient,
		BearerToken:      "test-token",
		Origin:           server.URL,
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
	})

	requestBody := semanticRequest{Name: "probe"}
	_, err = REST[semanticRequest, semanticResult](context.Background(), client, RESTRequest[semanticRequest]{
		Method: http.MethodPost,
		Path:   "/semantic",
		Body:   &requestBody,
	})
	require.ErrorIs(t, err, ErrSemanticFailure)

	result, err := REST[semanticRequest, semanticResult](context.Background(), client, RESTRequest[semanticRequest]{
		Method: http.MethodPost,
		Path:   "/semantic",
		Body:   &requestBody,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(42), result.ID)
}

func TestClient_RESTRejectsOversize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"data":{"id":42},"padding":"` + strings.Repeat("x", 256) + `"}`))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 64})
	_, err := REST[struct{}, semanticResult](context.Background(), client, RESTRequest[struct{}]{
		Method: http.MethodGet,
		Path:   "/oversize",
	})
	require.ErrorIs(t, err, ErrResponseTooLarge)
}

func TestClient_RESTClassifiesNonJSONStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte("unauthorized"))
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Config{BaseURL: server.URL, RequestTimeout: time.Second, MaxResponseBytes: 1024})
	_, err := REST[struct{}, semanticResult](context.Background(), client, RESTRequest[struct{}]{
		Method: http.MethodGet,
		Path:   "/unauthorized",
	})
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestClient_RESTRejectsCrossOriginRedirect(t *testing.T) {
	var foreignRequests atomic.Int32
	foreignServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		foreignRequests.Add(1)
	}))
	t.Cleanup(foreignServer.Close)
	dashboardServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, foreignServer.URL+"/credentials", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(dashboardServer.Close)

	client := newTestClient(t, Config{
		BaseURL:          dashboardServer.URL,
		BearerToken:      "redirect-secret",
		RequestTimeout:   time.Second,
		MaxResponseBytes: 1024,
	})
	_, err := REST[struct{}, semanticResult](context.Background(), client, RESTRequest[struct{}]{Method: http.MethodGet, Path: "/redirect"})
	require.ErrorIs(t, err, ErrRedirect)
	require.Zero(t, foreignRequests.Load())
}

func TestClient_RESTDeadline(t *testing.T) {
	// A tiny deadline races handler scheduling under -race; this barrier proves
	// the REST request is in flight before evaluating deadline behavior.
	requestEntered := make(chan struct{})
	requestCancelled := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestEntered)
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
		_, err := REST[struct{}, semanticResult](context.Background(), client, RESTRequest[struct{}]{
			Method: http.MethodGet,
			Path:   "/deadline",
		})
		result <- err
	}()
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-requestEntered:
	case <-waitContext.Done():
		t.Fatal("server did not receive REST request")
	}
	var err error
	select {
	case err = <-result:
	case <-waitContext.Done():
		t.Fatal("REST request did not reach its deadline")
	}
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-requestCancelled:
	case <-waitContext.Done():
		t.Fatal("server request context was not cancelled")
	}
}

func TestClient_RESTRejectsRedirectWithoutForwardingSensitiveHeaders(t *testing.T) {
	var redirected atomic.Int32
	foreignServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	t.Cleanup(foreignServer.Close)

	dashboardServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Redirect(writer, &http.Request{}, foreignServer.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(dashboardServer.Close)

	client := newTestClient(t, Config{BaseURL: dashboardServer.URL, BearerToken: "test-token", RequestTimeout: time.Second, MaxResponseBytes: 1024})
	_, err := REST[struct{}, semanticResult](context.Background(), client, RESTRequest[struct{}]{Method: http.MethodGet, Path: "/redirect"})
	require.ErrorIs(t, err, ErrRedirect)
	require.Zero(t, redirected.Load())
}

func newTestClient(t *testing.T, config Config) *Client {
	t.Helper()
	client, err := New(config)
	require.NoError(t, err)
	return client
}
