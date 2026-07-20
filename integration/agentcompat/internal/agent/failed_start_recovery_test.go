//go:build linux && agentcompat

package agent

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/stretchr/testify/require"
)

func TestAgent_StartExposesFinalizerWhenPreparedConsumerSurvivesRollback(t *testing.T) {
	prepared, err := PrepareBinary(t.Context(), testAgentSourceDir(t))
	require.NoError(t, err)
	trackingErr := errors.New("injected failed-start PID tracking error")
	var supervisor *processharness.Supervisor

	instance, startErr := Start(t.Context(), AgentStartConfig{
		PreparedBinary: prepared,
		Endpoint:       "127.0.0.1:1",
		UUID:           "00000000-0000-0000-0000-000000000200",
		newSupervisor: func(ctx context.Context, spec processharness.Spec) *processharness.Supervisor {
			supervisor = processharness.NewSupervisor(ctx, spec)
			cancelledContext, cancel := context.WithCancel(ctx)
			cancel()
			_ = supervisor.Stop(cancelledContext)
			require.NoError(t, supervisor.Stop(t.Context()))
			return supervisor
		},
		trackPID: func(int) error { return trackingErr },
	})
	require.Nil(t, instance)
	require.ErrorIs(t, startErr, trackingErr)
	require.NotNil(t, supervisor)
	pid := supervisor.PID()
	processGroupID := supervisor.ProcessGroupID()
	t.Cleanup(func() {
		_ = syscall.Kill(-processGroupID, syscall.SIGKILL)
		select {
		case <-supervisor.Exited():
		case <-time.After(5 * time.Second):
		}
		_ = prepared.Close()
	})
	require.NoError(t, syscall.Kill(-processGroupID, 0))

	var startFailure *AgentStartError
	require.ErrorAs(t, startErr, &startFailure)
	closeErr := prepared.Close()
	var usageErr *PreparedBinaryUsageError
	require.ErrorAs(t, closeErr, &usageErr)
	require.Equal(t, "has active consumers", usageErr.Reason)

	require.NoError(t, syscall.Kill(-processGroupID, syscall.SIGKILL))
	select {
	case <-supervisor.Exited():
	case <-time.After(5 * time.Second):
		t.Fatalf("failed-start consumer PID %d was not reaped", pid)
	}
	require.ErrorIs(t, syscall.Kill(-processGroupID, 0), syscall.ESRCH)
	require.NoError(t, startFailure.Finalize(t.Context()))
	require.NoError(t, startFailure.Finalize(t.Context()))
	require.NoError(t, prepared.Close())
}
