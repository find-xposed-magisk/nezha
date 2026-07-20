//go:build linux

package scenario

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func TestHeldIOStreamCapabilityWaitExpectationScopesToOwnedStream(t *testing.T) {
	tests := []struct {
		name      string
		absent    bool
		presentID string
		absentID  string
	}{
		{name: "live", presentID: "owned-stream"},
		{name: "cleanup", absent: true, absentID: "owned-stream"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var observed client.IOStreamStateExpectation
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				require.NoError(t, json.NewDecoder(request.Body).Decode(&observed))
				response.Header().Set("Content-Type", "application/json")
				_, err := response.Write([]byte(`{"success":true,"data":{"count":99,"generation":1}}`))
				require.NoError(t, err)
			}))
			defer server.Close()

			stateClient, err := client.New(client.Config{BaseURL: server.URL})
			require.NoError(t, err)
			capability := &heldIOStreamCapability{streamID: "owned-stream"}

			err = capability.waitExpectation(context.Background(), stateClient, client.IOStreamState{Count: 7}, test.absent)

			require.NoError(t, err)
			require.Nil(t, observed.ExpectedCount)
			require.Equal(t, test.presentID, observed.PresentStreamID)
			require.Equal(t, test.absentID, observed.AbsentStreamID)
		})
	}
}
