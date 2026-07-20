//go:build linux

package dashboard

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/testpaths"
	"github.com/stretchr/testify/require"
)

func TestDashboardRestart_PreservesFixtureIdentityAndCleansGenerations(t *testing.T) {
	// Given
	sourceDir, err := testpaths.NezhaSource(t.Name())
	require.NoError(t, err)
	dashboard, err := Start(t.Context(), StartConfig{SourceDir: sourceDir, EnableTLS: true, ReceiptGate: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, dashboard.Close(cleanupContext))
	})
	fixture := dashboard.FixtureIdentity()
	firstRuntime := dashboard.RuntimeIdentity()
	require.NotZero(t, fixture.HTTP.Inode)
	require.NotZero(t, fixture.HTTPS.Inode)

	// When
	stopContext, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	_, err = dashboard.StopProcess(stopContext)
	require.NoError(t, err)
	firstPID := firstRuntime.PID
	_, err = dashboard.StartProcess(stopContext)
	require.NoError(t, err)
	secondRuntime := dashboard.RuntimeIdentity()

	// Then
	require.Equal(t, fixture, dashboard.FixtureIdentity())
	require.NotEqual(t, firstRuntime.PID, secondRuntime.PID)
	require.Greater(t, secondRuntime.Generation, firstRuntime.Generation)
	require.Equal(t, fixture.HTTP.Address, dashboard.Endpoint())
	require.Equal(t, fixture.HTTP, dashboard.FixtureIdentity().HTTP)
	require.Equal(t, fixture.HTTPS, dashboard.FixtureIdentity().HTTPS)
	require.FileExists(t, fixture.DatabasePath)
	require.FileExists(t, fixture.ConfigPath)
	require.NoError(t, dashboard.Close(stopContext))
	require.Len(t, dashboard.CleanupReceipt().Processes, 2)
	require.NoFileExists(t, filepath.Join("/proc", strconv.Itoa(firstPID)))
	require.NoDirExists(t, fixture.WorkspaceRoot)
}
