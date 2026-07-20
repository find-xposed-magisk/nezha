//go:build linux && agentcompat

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPreparedBinary_EightIndependentAgentsShareBinaryAndCleanUp(t *testing.T) {
	prepared, err := PrepareBinary(t.Context(), testAgentSourceDir(t))
	require.NoError(t, err)
	preparedRoot := prepared.WorkspaceRoot()
	binaryPath := prepared.BinaryPath()
	require.FileExists(t, binaryPath)
	initialBinaryInfo, err := os.Stat(binaryPath)
	require.NoError(t, err)
	initialBinaryStat, ok := initialBinaryInfo.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	instances := make([]*Agent, 0, 8)
	t.Cleanup(func() {
		for _, instance := range instances {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = instance.Stop(cleanupContext)
			cancel()
		}
		_ = prepared.Close()
	})
	dashboardInstance := startTestDashboard(t, true)
	configPaths := make(map[string]struct{}, 8)
	logPaths := make(map[string]struct{}, 8)
	for index := range 8 {
		config := AgentStartConfig{
			PreparedBinary: prepared,
			Endpoint:       dashboardInstance.TLSEndpoint(),
			Secret:         dashboardInstance.AgentSecret(),
			UUID:           preparedBinaryUUID(index),
			TLS:            true,
			CAFilePath:     dashboardInstance.TLSCACertificatePath(),
			Debug:          index%2 == 0,
		}
		if index == 6 {
			config.FMObserverRunID = "prepared-binary-observer"
		}
		if index == 7 {
			config.Credential = &syscall.Credential{Uid: 65534, Gid: 65534}
		}
		instance, startErr := Start(t.Context(), config)
		require.NoError(t, startErr)
		require.Equal(t, binaryPath, instance.BinaryPath())
		processBinaryInfo, statErr := os.Stat(fmt.Sprintf("/proc/%d/exe", instance.PID()))
		if errors.Is(statErr, syscall.EACCES) || errors.Is(statErr, syscall.EPERM) {
			t.Logf("kernel denied /proc/%d/exe metadata for agent %d", instance.PID(), index)
		} else {
			require.NoError(t, statErr)
			processBinaryStat, statOK := processBinaryInfo.Sys().(*syscall.Stat_t)
			require.True(t, statOK)
			require.Equal(t, initialBinaryStat.Dev, processBinaryStat.Dev)
			require.Equal(t, initialBinaryStat.Ino, processBinaryStat.Ino)
		}
		require.NotEqual(t, preparedRoot, instance.WorkspaceRoot())
		require.NoFileExists(t, filepath.Join(instance.WorkspaceRoot(), "bin", "agent"))
		configPaths[instance.ConfigPath()] = struct{}{}
		logPaths[instance.LogPath()] = struct{}{}
		require.NoError(t, waitForAgentReady(t, instance, dashboardInstance))
		if index == 6 {
			require.NotNil(t, instance.FMProducerObserver())
			require.FileExists(t, instance.fmObserverPath)
		}
		if index == 7 {
			status, statusErr := os.ReadFile(fmt.Sprintf("/proc/%d/status", instance.PID()))
			require.NoError(t, statusErr)
			require.Contains(t, string(status), "Uid:\t65534\t65534\t65534\t65534")
		}
		instances = append(instances, instance)
	}
	finalBinaryInfo, err := os.Stat(binaryPath)
	require.NoError(t, err)
	require.True(t, os.SameFile(initialBinaryInfo, finalBinaryInfo))
	require.Len(t, configPaths, 8)
	require.Len(t, logPaths, 8)
	closeErr := prepared.Close()
	var usageErr *PreparedBinaryUsageError
	require.ErrorAs(t, closeErr, &usageErr)
	require.Equal(t, "close", usageErr.Operation)
	require.Equal(t, "has active consumers", usageErr.Reason)
	for index, instance := range instances {
		stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		require.NoError(t, instance.Stop(stopContext), "agent %d", index)
		cancel()
		require.True(t, instance.CleanupReceipt().Passed)
		require.False(t, instance.CleanupReceipt().Forced)
		if index < len(instances)-1 {
			require.DirExists(t, preparedRoot)
			require.FileExists(t, binaryPath)
		}
	}
	require.NoError(t, prepared.Close())
	require.NoError(t, prepared.Close())
	_, statErr := os.Stat(preparedRoot)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestAgent_StartBuildsAndOwnsItsBinary(t *testing.T) {
	instance, err := Start(t.Context(), AgentStartConfig{
		SourceDir: testAgentSourceDir(t),
		Endpoint:  "127.0.0.1:1",
		UUID:      "00000000-0000-0000-0000-000000000197",
	})
	require.NoError(t, err)
	workspaceRoot := instance.WorkspaceRoot()
	require.Equal(t, filepath.Join(workspaceRoot, "bin", "agent"), instance.BinaryPath())
	require.FileExists(t, instance.BinaryPath())
	stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, instance.Stop(stopContext))
	_, statErr := os.Stat(workspaceRoot)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestPreparedBinary_RejectsConsumerAfterClose(t *testing.T) {
	prepared, err := PrepareBinary(t.Context(), testAgentSourceDir(t))
	require.NoError(t, err)
	require.NoError(t, prepared.Close())
	_, err = Start(t.Context(), AgentStartConfig{
		PreparedBinary: prepared,
		Endpoint:       "127.0.0.1:1",
		UUID:           "00000000-0000-0000-0000-000000000198",
	})
	var usageErr *PreparedBinaryUsageError
	require.ErrorAs(t, err, &usageErr)
	require.Equal(t, "start", usageErr.Operation)
	require.Equal(t, "is closed", usageErr.Reason)
}

func TestPreparedBinary_RejectsInvalidSourceDirectory(t *testing.T) {
	_, err := PrepareBinary(t.Context(), "relative")
	var usageErr *PreparedBinaryUsageError
	require.ErrorAs(t, err, &usageErr)
	require.Equal(t, "prepare", usageErr.Operation)
	require.True(t, strings.Contains(usageErr.Reason, "must be absolute"))
}

func TestPreparedBinary_CancelledBuildRemovesWorkspace(t *testing.T) {
	temporaryRoot := t.TempDir()
	t.Setenv("TMPDIR", temporaryRoot)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := PrepareBinary(ctx, testAgentSourceDir(t))
	require.Error(t, err)
	entries, readErr := os.ReadDir(temporaryRoot)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestPreparedBinary_RejectsUninitializedValue(t *testing.T) {
	_, err := Start(t.Context(), AgentStartConfig{
		PreparedBinary: &PreparedBinary{},
		Endpoint:       "127.0.0.1:1",
		UUID:           "00000000-0000-0000-0000-000000000199",
	})
	var usageErr *PreparedBinaryUsageError
	require.ErrorAs(t, err, &usageErr)
	require.Equal(t, "start", usageErr.Operation)
	require.Equal(t, "is uninitialized", usageErr.Reason)
}

func TestPreparedBinary_CloseRejectsNilAndUninitializedValues(t *testing.T) {
	var nilPrepared *PreparedBinary
	for _, prepared := range []*PreparedBinary{nilPrepared, &PreparedBinary{}} {
		err := prepared.Close()
		var usageErr *PreparedBinaryUsageError
		require.ErrorAs(t, err, &usageErr)
		require.Equal(t, "close", usageErr.Operation)
	}
}

func preparedBinaryUUID(index int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", 190+index)
}
