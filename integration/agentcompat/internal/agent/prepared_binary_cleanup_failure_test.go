//go:build linux && agentcompat

package agent

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestPreparedBinary_ReleasesLeaseWhenOnlyWorkspaceCleanupFails(t *testing.T) {
	prepared, err := PrepareBinary(t.Context(), testAgentSourceDir(t))
	require.NoError(t, err)
	_, release, err := prepared.acquire()
	require.NoError(t, err)
	workspaceRoot, err := workspace.New(context.WithoutCancel(t.Context()))
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ownedListener, err := workspaceRoot.AdoptListener(listener)
	require.NoError(t, err)
	heldListener, err := ownedListener.ExtraFile()
	require.NoError(t, err)

	agent := &Agent{workspace: workspaceRoot, releaseBinary: release, releasePending: true, cleanupDone: make(chan struct{})}
	stopErr := agent.Stop(t.Context())
	require.Error(t, stopErr)
	require.NoError(t, prepared.Close())
	require.NoError(t, heldListener.Close())
	require.NoError(t, workspaceRoot.Close())
	require.NoDirExists(t, workspaceRoot.Root())
}

func TestPreparedBinary_RetainsLeaseWhileConsumerProcessGroupLivesAfterCleanupFailure(t *testing.T) {
	prepared, err := PrepareBinary(t.Context(), testAgentSourceDir(t))
	require.NoError(t, err)
	binaryPath, release, err := prepared.acquire()
	require.NoError(t, err)
	workspaceRoot, err := workspace.New(context.WithoutCancel(t.Context()))
	require.NoError(t, err)

	supervisor := processharness.NewSupervisor(t.Context(), processharness.Spec{
		Name: "prepared-binary-lingering-consumer", Path: "/bin/sh", Args: []string{"-c", "exec tail -f /dev/null"},
		MaxLogBytes: 1024, TerminateTimeout: time.Second, KillTimeout: time.Second,
	})
	cancelledContext, cancel := context.WithCancel(t.Context())
	cancel()
	_ = supervisor.Stop(cancelledContext)
	require.NoError(t, supervisor.Stop(t.Context()))
	require.NoError(t, supervisor.Start())
	pid := supervisor.PID()
	pgid := supervisor.ProcessGroupID()
	require.NoError(t, workspaceRoot.TrackPID(pid))
	require.NoError(t, workspaceRoot.TrackProcessGroup(pgid))

	agent := &Agent{
		workspace: workspaceRoot, binaryPath: binaryPath, releaseBinary: release, releasePending: true,
		cleanupDone: make(chan struct{}), processes: []*processGeneration{{supervisor: supervisor, identity: ProcessIdentity{PID: pid, ProcessGroupID: pgid}}},
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		select {
		case <-supervisor.Exited():
		case <-time.After(5 * time.Second):
		}
		_ = workspaceRoot.Close()
		_ = prepared.Close()
	})

	stopErr := agent.Stop(t.Context())
	require.Error(t, stopErr)
	firstStopError := stopErr.Error()
	require.NoError(t, syscall.Kill(-pgid, 0))
	require.FileExists(t, binaryPath)
	closeErr := prepared.Close()
	var usageErr *PreparedBinaryUsageError
	require.ErrorAs(t, closeErr, &usageErr)
	require.Equal(t, "has active consumers", usageErr.Reason)

	require.NoError(t, syscall.Kill(-pgid, syscall.SIGKILL))
	select {
	case <-supervisor.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("lingering consumer process was not reaped")
	}
	require.True(t, errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH))
	recoveryErr := agent.Stop(t.Context())
	require.Error(t, recoveryErr)
	require.Equal(t, firstStopError, recoveryErr.Error())
	require.NoError(t, prepared.Close())
	_, statErr := os.Stat(prepared.WorkspaceRoot())
	require.ErrorIs(t, statErr, os.ErrNotExist)
}
