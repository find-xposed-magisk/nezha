//go:build linux && agentcompat

package scenario

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/stretchr/testify/require"
)

func TestReconnectScenario_RealDashboardAndAgentProcessRestarts(t *testing.T) {
	// Given
	nezhaSource := os.Getenv("AGENTCOMPAT_NEZHA_SOURCE")
	agentSource := os.Getenv("AGENTCOMPAT_AGENT_SOURCE")
	if nezhaSource == "" || agentSource == "" {
		t.Skip("set AGENTCOMPAT_NEZHA_SOURCE and AGENTCOMPAT_AGENT_SOURCE")
	}
	evidenceDirectory := os.Getenv("AGENTCOMPAT_RECONNECT_EVIDENCE_DIR")
	if evidenceDirectory == "" {
		evidenceDirectory = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(evidenceDirectory, 0o700))
	paths, err := contract.NewPaths(nezhaSource, agentSource, evidenceDirectory)
	require.NoError(t, err)
	testContext, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	// When
	result, reconnectEvidence, err := (Reconnect{}).RunWithEvidence(testContext, ReconnectInput{Paths: paths})

	// Then
	for _, assertion := range result.Assertions {
		t.Logf("assertion=%q passed=%t details=%q", assertion.Name, assertion.Passed, assertion.Details)
	}
	t.Logf("agent cleanup=%+v dashboard cleanup=%+v", reconnectEvidence.AgentCleanup, reconnectEvidence.DashboardCleanup)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.True(t, result.CleanupOK)
	require.NoError(t, reconnectEvidence.Validate())
	require.True(t, reconnectEvidence.AgentCleanup.Passed)
	require.False(t, reconnectEvidence.AgentCleanup.Forced)
	require.Len(t, reconnectEvidence.AgentCleanup.Processes, 2)
	require.True(t, reconnectEvidence.DashboardCleanup.Passed)
	require.False(t, reconnectEvidence.DashboardCleanup.Forced)
	require.Len(t, reconnectEvidence.DashboardCleanup.Processes, 2)
	require.True(t, reconnectEvidence.Identity.DashboardFixtureUnchanged)
	require.NotZero(t, reconnectEvidence.Fixture.Dashboard.HTTP.Inode)
	require.NotZero(t, reconnectEvidence.Fixture.Dashboard.Receipt.Inode)
	require.Greater(t, reconnectEvidence.Runtime.DashboardAfter.Generation, reconnectEvidence.Runtime.DashboardBefore.Generation)
	require.NotEqual(t, reconnectEvidence.Runtime.DashboardBefore.PID, reconnectEvidence.Runtime.DashboardAfter.PID)
	require.Greater(t, reconnectEvidence.Runtime.AgentAfter.Generation, reconnectEvidence.Runtime.AgentBefore.Generation)
	require.NotEqual(t, reconnectEvidence.Runtime.AgentBefore.PID, reconnectEvidence.Runtime.AgentAfter.PID)
	require.Positive(t, reconnectEvidence.Lifecycle.ReconnectInterval)
	require.Zero(t, reconnectEvidence.Lifecycle.StaleGenerationReceipts)
	require.Zero(t, reconnectEvidence.Lifecycle.DuplicateTaskIDs)
	require.Zero(t, reconnectEvidence.Lifecycle.LostResultIDs)
	require.Len(t, reconnectEvidence.Observation.TaskIDs, 5)
	require.Equal(t, reconnectEvidence.Observation.TaskIDs, reconnectEvidence.Observation.ResultIDs)

	artifactPath := filepath.Join(evidenceDirectory, "reconnect-real-process.json")
	artifact, err := json.MarshalIndent(struct {
		Result   Result            `json:"result"`
		Evidence ReconnectEvidence `json:"evidence"`
	}{Result: result, Evidence: reconnectEvidence}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(artifactPath, append(artifact, '\n'), 0o600))
	readArtifact, err := os.ReadFile(artifactPath)
	require.NoError(t, err)
	var recorded struct {
		Result   Result            `json:"result"`
		Evidence ReconnectEvidence `json:"evidence"`
	}
	require.NoError(t, json.Unmarshal(readArtifact, &recorded))
	require.Equal(t, result, recorded.Result)
	require.Equal(t, reconnectEvidence, recorded.Evidence)
	t.Logf("reconnect evidence artifact: %s", artifactPath)
}
