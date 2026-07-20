package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientIOStreamStateHelpersUseTypedRESTContracts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			require.Equal(t, "/agentcompat/io-stream-state", request.URL.Path)
			_, err := writer.Write([]byte(`{"success":true,"data":{"count":0,"generation":4}}`))
			require.NoError(t, err)
		case http.MethodPost:
			var payload map[string]json.RawMessage
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
			var expectedCount int
			require.NoError(t, json.Unmarshal(payload["expected_count"], &expectedCount))
			require.Equal(t, 0, expectedCount)
			var absentStreamID string
			require.NoError(t, json.Unmarshal(payload["absent_stream_id"], &absentStreamID))
			require.Equal(t, "stream-id", absentStreamID)
			var presentStreamID string
			require.NoError(t, json.Unmarshal(payload["present_stream_id"], &presentStreamID))
			require.Equal(t, "present-id", presentStreamID)
			_, err := writer.Write([]byte(`{"success":true,"data":{"count":0,"generation":5}}`))
			require.NoError(t, err)
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	httpClient := newTestClient(t, Config{BaseURL: server.URL})
	snapshot, err := httpClient.IOStreamState(context.Background())
	require.NoError(t, err)
	require.Equal(t, IOStreamState{Count: 0, Generation: 4}, snapshot)
	waited, err := httpClient.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0), PresentStreamID: "present-id", AbsentStreamID: "stream-id"})
	require.NoError(t, err)
	require.Equal(t, IOStreamState{Count: 0, Generation: 5}, waited)
}

func TestClientIOStreamStateExpectationJSONPresence(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		requestCount++
		if requestCount == 2 {
			require.NotContains(t, payload, "expected_count")
		} else {
			value, exists := payload["expected_count"]
			require.True(t, exists)
			var count int
			require.NoError(t, json.Unmarshal(value, &count))
			require.Zero(t, count)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(`{"success":true,"data":{"count":0,"generation":1}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	httpClient := newTestClient(t, Config{BaseURL: server.URL})
	_, err := httpClient.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0)})
	require.NoError(t, err)
	_, err = REST[IOStreamStateExpectation, IOStreamState](context.Background(), httpClient, RESTRequest[IOStreamStateExpectation]{
		Method: http.MethodPost,
		Path:   "/agentcompat/io-stream-state",
		Body:   &IOStreamStateExpectation{AbsentStreamID: "stream-id"},
	})
	require.NoError(t, err)
}

func TestClientIOStreamStateMapsSemanticFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(`{"success":false,"error":"invalid expectation"}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	httpClient := newTestClient(t, Config{BaseURL: server.URL})
	state, err := httpClient.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{AbsentStreamID: "private-stream-id"})
	require.ErrorIs(t, err, ErrSemanticFailure)
	require.Zero(t, state)
}

func TestClientIOStreamStateMapsUnauthorizedGETAndPOST(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(server.Close)
			httpClient := newTestClient(t, Config{BaseURL: server.URL})
			var state IOStreamState
			var err error
			if method == http.MethodGet {
				state, err = httpClient.IOStreamState(context.Background())
			} else {
				state, err = httpClient.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0)})
			}
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrUnauthorized))
			require.Zero(t, state)
		})
	}
}
