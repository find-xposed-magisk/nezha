//go:build linux && agentcompat

package scenario

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/stretchr/testify/require"
)

func TestReconnectScenario_DashboardExitFaultCleansRealProcesses(t *testing.T) {
	// Given
	nezhaSource := os.Getenv("AGENTCOMPAT_NEZHA_SOURCE")
	agentSource := os.Getenv("AGENTCOMPAT_AGENT_SOURCE")
	if nezhaSource == "" || agentSource == "" {
		t.Skip("set AGENTCOMPAT_NEZHA_SOURCE and AGENTCOMPAT_AGENT_SOURCE")
	}
	evidenceDirectory := os.Getenv("AGENTCOMPAT_RECONNECT_FAULT_EVIDENCE_DIR")
	if evidenceDirectory == "" {
		evidenceDirectory = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(evidenceDirectory, 0o700))
	paths, err := contract.NewPaths(nezhaSource, agentSource, evidenceDirectory)
	require.NoError(t, err)
	testContext, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	// When
	result, reconnectEvidence, runErr := (Reconnect{}).RunWithEvidence(testContext, ReconnectInput{Paths: paths, DashboardFault: "dashboard-exit"})

	// Then
	require.ErrorIs(t, runErr, ErrReconnectDashboardExitFault)
	require.Equal(t, reconnectScenarioName, result.Name)
	require.False(t, result.Passed)
	require.True(t, result.CleanupOK)
	require.Contains(t, result.Error, ErrReconnectDashboardExitFault.Error())
	require.Len(t, result.Assertions, 3)
	require.True(t, result.Assertions[0].Passed)
	require.Equal(t, "Dashboard disconnect barrier stopped generation one", result.Assertions[0].Name)
	require.True(t, result.Assertions[1].Passed)
	require.Equal(t, "outside-root sentinel remains unchanged", result.Assertions[1].Name)
	require.True(t, result.Assertions[2].Passed)
	require.Equal(t, "multi-generation process listener and workspace cleanup completed", result.Assertions[2].Name)
	require.True(t, reconnectEvidence.Lifecycle.OutsideRootSentinelUnchanged)
	require.True(t, reconnectEvidence.AgentCleanup.Passed)
	require.False(t, reconnectEvidence.AgentCleanup.Forced)
	require.Len(t, reconnectEvidence.AgentCleanup.Processes, 1)
	require.True(t, reconnectEvidence.DashboardCleanup.Passed)
	require.False(t, reconnectEvidence.DashboardCleanup.Forced)
	require.Len(t, reconnectEvidence.DashboardCleanup.Processes, 1)
	require.Zero(t, reconnectEvidence.Runtime.DashboardAfter)
	require.Zero(t, reconnectEvidence.Runtime.AgentAfter)
	require.NoDirExists(t, reconnectEvidence.Fixture.AgentRoot)
	require.NoDirExists(t, reconnectEvidence.Fixture.Dashboard.WorkspaceRoot)
	for _, cleanupReceipt := range []struct {
		name      string
		processes []processharness.CleanupRecord
	}{
		{name: "Agent", processes: reconnectEvidence.AgentCleanup.Processes},
		{name: "Dashboard", processes: reconnectEvidence.DashboardCleanup.Processes},
	} {
		for _, process := range cleanupReceipt.processes {
			require.NoDirExists(t, filepath.Join("/proc", strconv.Itoa(process.PID)), "%s process %d survived cleanup", cleanupReceipt.name, process.PID)
		}
	}
	for _, listenerIdentity := range []struct {
		name    string
		address string
	}{
		{name: "Dashboard HTTP", address: reconnectEvidence.Fixture.Dashboard.HTTP.Address},
		{name: "Dashboard receipt", address: reconnectEvidence.Fixture.Dashboard.Receipt.Address},
	} {
		listener, listenErr := net.Listen("tcp", listenerIdentity.address)
		require.NoError(t, listenErr, "%s listener was not released", listenerIdentity.name)
		require.NoError(t, listener.Close())
	}

	artifactPath := filepath.Join(evidenceDirectory, "reconnect-dashboard-exit-real-process.json")
	type faultArtifact struct {
		Result   Result            `json:"result"`
		Evidence ReconnectEvidence `json:"evidence"`
		Error    string            `json:"error"`
	}
	recordedArtifact := faultArtifact{Result: result, Evidence: reconnectEvidence, Error: runErr.Error()}
	artifact, err := json.MarshalIndent(recordedArtifact, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(artifactPath, append(artifact, '\n'), 0o600))
	artifactInfo, err := os.Stat(artifactPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), artifactInfo.Mode().Perm())
	readArtifact, err := os.ReadFile(artifactPath)
	require.NoError(t, err)
	var decodedArtifact faultArtifact
	require.NoError(t, json.Unmarshal(readArtifact, &decodedArtifact))
	require.Equal(t, recordedArtifact, decodedArtifact)
	t.Logf("reconnect Dashboard-exit fault artifact: %s", artifactPath)
}
