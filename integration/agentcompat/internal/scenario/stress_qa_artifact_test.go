//go:build linux

package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestStress_WriteTypedQAArtifact(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	fixture := stressDashboardResourceFixture(100)
	evaluation, err := EvaluateStressResource(fixture)
	require.NoError(t, err)
	artifact := struct {
		Plan     StressPlan               `json:"plan"`
		Resource StressResourceEvaluation `json:"resource"`
	}{Plan: plan, Resource: evaluation}
	data, err := json.MarshalIndent(artifact, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "stress-contracts.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	require.Contains(t, string(data), `"profile": "pr-full"`)
	require.Contains(t, string(data), `"rounds"`)
	require.Contains(t, string(data), `"rss_limit_bytes": 67108864`)
	require.NotEmpty(t, path)
}
