//go:build linux && agentcompat

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAgent_RejectsMalformedStartWithoutWorkspaceArtifact(t *testing.T) {
	// Given
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)

	// When
	_, err := Start(t.Context(), AgentStartConfig{SourceDir: filepath.Join(parent, "missing"), Endpoint: "127.0.0.1:1", UUID: "bad"})

	// Then
	require.Error(t, err)
	entries, readErr := os.ReadDir(parent)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestAgent_ContextInterruptionCleansWorkspace(t *testing.T) {
	// Given
	processContext, interrupt := context.WithCancel(t.Context())
	dashboardInstance := startTestDashboard(t, false)
	agentInstance, err := Start(processContext, AgentStartConfig{
		SourceDir: testAgentSourceDir(t),
		Endpoint:  dashboardInstance.Endpoint(),
		Secret:    dashboardInstance.AgentSecret(),
		UUID:      "00000000-0000-0000-0000-000000000085",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, agentInstance.Stop(cleanupContext))
	})
	root := agentInstance.WorkspaceRoot()

	// When
	interrupt()

	// Then
	select {
	case <-agentInstance.CleanupDone():
	case <-time.After(30 * time.Second):
		t.Fatal("agent cleanup did not complete")
	}
	_, err = os.Stat(root)
	require.ErrorIs(t, err, os.ErrNotExist)
}
