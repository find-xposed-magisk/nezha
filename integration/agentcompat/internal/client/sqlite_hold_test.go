package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientSQLiteHoldHelpersUseTypedRESTContracts(t *testing.T) {
	// Given
	receiptID := "ERERERERERERERERERERERERERERERERERERERERERE"
	requests := make([]string, 0, 6)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		var payload map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		state := SQLiteHoldStateArmed
		switch request.URL.Path {
		case "/agentcompat/sqlite-hold/arm":
			require.Empty(t, payload)
		case "/agentcompat/sqlite-hold/wait":
			require.JSONEq(t, `"`+receiptID+`"`, string(payload["id"]))
			require.NoError(t, json.Unmarshal(payload["state"], &state))
			require.Contains(t, []SQLiteHoldState{SQLiteHoldStateSelected, SQLiteHoldStateFinalizing}, state)
		case "/agentcompat/sqlite-hold/snapshot":
			require.Len(t, payload, 1)
			state = SQLiteHoldStateSelected
		case "/agentcompat/sqlite-hold/release":
			require.Len(t, payload, 1)
			state = SQLiteHoldStateReleased
		case "/agentcompat/sqlite-hold/abort":
			require.Len(t, payload, 1)
			state = SQLiteHoldStateAborted
		default:
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(`{"success":true,"data":{"id":"` + receiptID + `","state":"` + string(state) + `"}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	httpClient := newTestClient(t, Config{BaseURL: server.URL})

	// When
	armed, err := httpClient.ArmSQLiteHold(context.Background())
	require.NoError(t, err)
	selected, err := httpClient.WaitForSQLiteHold(context.Background(), armed, SQLiteHoldStateSelected)
	require.NoError(t, err)
	finalizing, err := httpClient.WaitForSQLiteHold(context.Background(), selected, SQLiteHoldStateFinalizing)
	require.NoError(t, err)
	snapshot, err := httpClient.SnapshotSQLiteHold(context.Background(), finalizing)
	require.NoError(t, err)
	released, err := httpClient.ReleaseSQLiteHold(context.Background(), snapshot)
	require.NoError(t, err)
	aborted, err := httpClient.AbortSQLiteHold(context.Background(), released)

	// Then
	require.NoError(t, err)
	require.Equal(t, SQLiteHoldStateArmed, armed.State)
	require.Equal(t, SQLiteHoldStateSelected, selected.State)
	require.Equal(t, SQLiteHoldStateFinalizing, finalizing.State)
	require.Equal(t, SQLiteHoldStateSelected, snapshot.State)
	require.Equal(t, SQLiteHoldStateReleased, released.State)
	require.Equal(t, SQLiteHoldStateAborted, aborted.State)
	require.Equal(t, []string{
		"/agentcompat/sqlite-hold/arm",
		"/agentcompat/sqlite-hold/wait",
		"/agentcompat/sqlite-hold/wait",
		"/agentcompat/sqlite-hold/snapshot",
		"/agentcompat/sqlite-hold/release",
		"/agentcompat/sqlite-hold/abort",
	}, requests)
}
