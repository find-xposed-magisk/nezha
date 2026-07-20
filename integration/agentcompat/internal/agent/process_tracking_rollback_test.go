//go:build linux && agentcompat

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

func TestAgent_StartProcess_tracksStartedGenerationForStop(t *testing.T) {
	agent := newUnstartedTestAgent(t)
	transition, err := agent.StartProcess(t.Context())
	require.NoError(t, err)
	require.NotZero(t, transition.Current.PID)
	require.Equal(t, transition.Current, agent.RuntimeIdentity())
	require.NoError(t, agent.Stop(t.Context()))
}

func TestAgent_StartProcess_rollsBackStartedGenerationWhenPIDTrackingFails(t *testing.T) {
	agent := newUnstartedTestAgent(t)
	trackingErr := errors.New("injected PID tracking failure")
	var startedPID int
	agent.trackPID = func(pid int) error {
		startedPID = pid
		return trackingErr
	}
	_, err := agent.StartProcess(t.Context())
	require.ErrorIs(t, err, trackingErr)
	require.Empty(t, agent.RuntimeIdentity())
	receipt := agent.CleanupReceipt()
	require.Len(t, receipt.Processes, 1)
	t.Logf("started_pid=%d started_pgid=%d injected_failure=%q runtime_identity=%+v cleanup_record=%+v forced=%t", startedPID, startedPID, trackingErr, agent.RuntimeIdentity(), receipt.Processes[0], receipt.Forced)
	require.Equal(t, "agent", receipt.Processes[0].Name)
	require.NotZero(t, receipt.Processes[0].PID)
	require.False(t, receipt.Processes[0].Forced)
	requireProcessAndGroupGone(t, receipt.Processes[0].PID, receipt.Processes[0].PID)
	require.FileExists(t, agent.ConfigPath())
	require.NoError(t, agent.Stop(t.Context()))
	require.NoDirExists(t, agent.WorkspaceRoot())
}

func TestAgent_StartProcess_rollsBackStartedGenerationWhenProcessGroupTrackingFails(t *testing.T) {
	agent := newUnstartedTestAgent(t)
	trackingErr := errors.New("injected process group tracking failure")
	var startedPID, startedProcessGroupID int
	agent.trackPID = func(pid int) error {
		startedPID = pid
		return nil
	}
	agent.trackProcessGroup = func(processGroupID int) error {
		startedProcessGroupID = processGroupID
		return trackingErr
	}
	_, err := agent.StartProcess(t.Context())
	require.ErrorIs(t, err, trackingErr)
	require.Empty(t, agent.RuntimeIdentity())
	receipt := agent.CleanupReceipt()
	require.Len(t, receipt.Processes, 1)
	t.Logf("started_pid=%d started_pgid=%d injected_failure=%q runtime_identity=%+v cleanup_record=%+v forced=%t", startedPID, startedProcessGroupID, trackingErr, agent.RuntimeIdentity(), receipt.Processes[0], receipt.Forced)
	require.Equal(t, "agent", receipt.Processes[0].Name)
	require.NotZero(t, receipt.Processes[0].PID)
	require.False(t, receipt.Processes[0].Forced)
	requireProcessAndGroupGone(t, receipt.Processes[0].PID, startedProcessGroupID)
	require.FileExists(t, agent.ConfigPath())
	require.NoError(t, agent.Stop(t.Context()))
	require.NoDirExists(t, agent.WorkspaceRoot())
}

func newUnstartedTestAgent(t *testing.T) *Agent {
	workspaceRoot, err := workspace.New(t.Context())
	require.NoError(t, err)
	agent := &Agent{workspace: workspaceRoot, uuid: "00000000-0000-0000-0000-000000000091", cleanupDone: make(chan struct{}), startConfig: AgentStartConfig{SourceDir: testAgentSourceDir(t), Endpoint: "127.0.0.1:1", Secret: agentSecret}, trackPID: workspaceRoot.TrackPID, trackProcessGroup: workspaceRoot.TrackProcessGroup}
	require.NoError(t, agent.prepareFixture(t.Context(), agent.startConfig))
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, agent.Stop(cleanupContext))
	})
	return agent
}

func requireProcessAndGroupGone(t *testing.T, pid, processGroupID int) {
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorIs(t, syscall.Kill(-processGroupID, 0), syscall.ESRCH)
}

func TestAgent_StopProcessPreservesConfig(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, false)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{UUID: "00000000-0000-0000-0000-000000000089"})
	require.NoError(t, waitForAgentReady(t, agentInstance, dashboardInstance))
	configBefore, err := os.ReadFile(agentInstance.ConfigPath())
	require.NoError(t, err)
	pidBefore := agentInstance.PID()

	// When
	stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = agentInstance.StopProcess(stopContext)

	// Then
	require.NoError(t, err)
	configAfter, readErr := os.ReadFile(agentInstance.ConfigPath())
	require.NoError(t, readErr)
	require.Equal(t, configBefore, configAfter)
	require.Equal(t, "00000000-0000-0000-0000-000000000089", agentInstance.UUID())
	require.NotZero(t, pidBefore)
}

func TestAgent_RestartProcessPreservesConfigBytesAndUUID(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, false)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{UUID: "00000000-0000-0000-0000-000000000090"})
	require.NoError(t, waitForAgentReady(t, agentInstance, dashboardInstance))
	configBefore, err := os.ReadFile(agentInstance.ConfigPath())
	require.NoError(t, err)
	pidBefore := agentInstance.PID()

	// When
	restartContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = agentInstance.RestartProcess(restartContext)

	// Then
	require.NoError(t, err)
	require.NotEqual(t, pidBefore, agentInstance.PID())
	configAfter, readErr := os.ReadFile(agentInstance.ConfigPath())
	require.NoError(t, readErr)
	require.Equal(t, configBefore, configAfter)
	require.Equal(t, "00000000-0000-0000-0000-000000000090", agentInstance.UUID())
}
