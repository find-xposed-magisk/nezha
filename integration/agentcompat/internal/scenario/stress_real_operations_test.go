//go:build linux && agentcompat

package scenario

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func TestStressFilesystemUsesAgentPAT(t *testing.T) {
	fixture := &heldSessionSetRealFixture{
		agentPATs: []heldRealPATIdentity{{TokenID: 1}},
	}
	operation := StressOperationPlan{Agent: mustStressAgentOrdinal(t, 1), PAT: mustStressPATID(t, "pat-1"), Kind: StressOperationFilesystem}

	fixture.plan = StressPlan{Rounds: []StressRoundPlan{{Operations: []StressOperationPlan{operation}}}}
	selected, err := stressOperationPATIdentity(fixture, operation)
	require.NoError(t, err)
	require.Equal(t, uint64(1), selected.TokenID)
}

func TestStressExecUsesAgentPAT(t *testing.T) {
	fixture := &heldSessionSetRealFixture{
		agentPATs: []heldRealPATIdentity{{TokenID: 1}},
	}
	operation := StressOperationPlan{Agent: mustStressAgentOrdinal(t, 1), PAT: mustStressPATID(t, "pat-1"), Kind: StressOperationExec}

	fixture.plan = StressPlan{Rounds: []StressRoundPlan{{Operations: []StressOperationPlan{operation}}}}
	selected, err := stressOperationPATIdentity(fixture, operation)
	require.NoError(t, err)
	require.Equal(t, uint64(1), selected.TokenID)
}

func TestStressFilesystemProofDispatchesOneWrite(t *testing.T) {
	requests := make([]string, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var envelope struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&envelope))
		requests = append(requests, envelope.Params.Name)
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[],"structuredContent":{"size":35,"sha256":"6ececdd71257073948afc9c699d12d3075d05d3dc339c4511496c3fbc27a2081"}}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	mcpClient, err := client.New(client.Config{BaseURL: server.URL})
	require.NoError(t, err)
	parent := t.TempDir()

	proof, err := executeStressFilesystemProof(t.Context(), mcpClient, 17, parent, "operation", 1)

	require.NoError(t, err)
	require.Equal(t, stressProof("agentcompat-stress-filesystem-proof"), proof)
	require.Equal(t, []string{"fs.write"}, requests)
}

func mustStressAgentOrdinal(t *testing.T, value int) StressAgentOrdinal {
	t.Helper()
	ordinal, err := NewStressAgentOrdinal(value)
	require.NoError(t, err)
	return ordinal
}

func mustStressPATID(t *testing.T, value string) StressPATID {
	t.Helper()
	pat, err := NewStressPATID(value)
	require.NoError(t, err)
	return pat
}
