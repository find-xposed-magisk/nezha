//go:build linux

package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/testpaths"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

type failingDashboardSupervisor struct{}

func (failingDashboardSupervisor) Start() error { return nil }

func (failingDashboardSupervisor) Stop(context.Context) error {
	return errors.New("injected supervisor cleanup failure")
}

func (failingDashboardSupervisor) Exited() <-chan struct{} {
	return make(chan struct{})
}

func (failingDashboardSupervisor) PID() int { return 0 }

func (failingDashboardSupervisor) ProcessGroupID() int { return 0 }

func (failingDashboardSupervisor) CleanupRecord() processharness.CleanupRecord {
	return processharness.CleanupRecord{Name: "dashboard", Error: "injected supervisor cleanup failure"}
}

func TestDashboardAdversarial_RecreatesFreshStateAfterShutdown(t *testing.T) {
	// Given
	first := startDashboardWithoutCleanup(t, false)
	firstDatabasePath := first.DatabasePath()
	firstWorkspaceRoot := first.WorkspaceRoot()
	stopContext, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	require.NoError(t, first.Stop(stopContext))
	cancel()

	// When
	second := startDashboard(t, false)

	// Then
	require.NotEqual(t, firstDatabasePath, second.DatabasePath())
	require.NotEqual(t, firstWorkspaceRoot, second.WorkspaceRoot())
	require.NoDirExists(t, firstWorkspaceRoot)
	require.True(t, second.Bootstrap().LoginAuthenticated)
}

func TestDashboardAdversarial_ContextInterruptionCleansProcessAndWorkspace(t *testing.T) {
	// Given
	processContext, interrupt := context.WithCancel(t.Context())
	sourceDir, err := testpaths.NezhaSource(t.Name())
	require.NoError(t, err)
	dashboard, err := Start(processContext, StartConfig{SourceDir: sourceDir})
	require.NoError(t, err)
	root := dashboard.WorkspaceRoot()
	pid := dashboard.PID()
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, dashboard.Stop(cleanupContext))
	})

	// When
	interrupt()
	requireDashboardCleanup(t, dashboard)

	// Then
	require.NoDirExists(t, root)
	require.NoFileExists(t, filepath.Join("/proc", strconv.Itoa(pid)))
	require.True(t, dashboard.CleanupReceipt().Passed)
	require.False(t, dashboard.CleanupReceipt().Forced)
}

func TestDashboardAdversarial_StopDeadlineStillCleansProcessAndWorkspace(t *testing.T) {
	// Given
	dashboard := startDashboardWithoutCleanup(t, false)
	root := dashboard.WorkspaceRoot()
	pid := dashboard.PID()
	stopContext, cancel := context.WithCancel(t.Context())
	cancel()
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		require.NoError(t, dashboard.Stop(cleanupContext))
	})

	// When
	stopError := dashboard.Stop(stopContext)

	// Then
	require.ErrorIs(t, stopError, context.Canceled)
	requireDashboardCleanup(t, dashboard)
	require.NoDirExists(t, root)
	require.NoFileExists(t, filepath.Join("/proc", strconv.Itoa(pid)))
	require.True(t, dashboard.CleanupReceipt().Passed)
	require.False(t, dashboard.CleanupReceipt().Forced)
}

func TestDashboardAdversarial_SupervisorErrorStillClosesWorkspace(t *testing.T) {
	// Given
	workspaceRoot, err := workspace.New(t.Context())
	require.NoError(t, err)
	dashboard := &Dashboard{
		workspace:   workspaceRoot,
		supervisor:  failingDashboardSupervisor{},
		cleanupDone: make(chan struct{}),
	}

	// When
	stopContext, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stopError := dashboard.Stop(stopContext)

	// Then
	require.ErrorContains(t, stopError, "injected supervisor cleanup failure")
	require.NoDirExists(t, workspaceRoot.Root())
	require.False(t, dashboard.CleanupReceipt().Passed)
}

func TestDashboardAdversarial_HungProcessRespectsReadinessDeadline(t *testing.T) {
	// Given
	workspaceParent := t.TempDir()
	t.Setenv("TMPDIR", workspaceParent)
	sourceDir := writeHungDashboardSource(t)
	requireNoWorkspaceEntries(t, workspaceParent)

	// When
	_, err := Start(t.Context(), StartConfig{
		SourceDir:        sourceDir,
		ReadinessTimeout: 200 * time.Millisecond,
	})

	// Then
	require.ErrorContains(t, err, "dashboard login readiness")
	requireNoWorkspaceEntries(t, workspaceParent)
}

func TestDashboardAdversarial_RejectsMisleadingHTTP200Login(t *testing.T) {
	// Given
	dashboard := startDashboard(t, false)
	requestBody := []byte(`{"username":"admin","password":"wrong-password"}`)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, dashboard.URL()+"/api/v1/login", bytes.NewReader(requestBody))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")

	// When
	response, err := dashboard.restHTTPClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	require.NoError(t, err)
	var envelope client.CommonResponse[json.RawMessage]
	require.NoError(t, json.Unmarshal(responseBody, &envelope))

	// Then
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.False(t, envelope.Success)
	require.Contains(t, envelope.Error, "Unauthorized")
}

func writeHungDashboardSource(t *testing.T) string {
	t.Helper()
	sourceDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sourceDir, "cmd", "dashboard"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "go.mod"), []byte("module example.com/hungdashboard\n\ngo 1.23\n"), 0o600))
	program := `package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	<-signals
}
`
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "cmd", "dashboard", "main.go"), []byte(program), 0o600))
	return sourceDir
}

func requireNoWorkspaceEntries(t *testing.T, workspaceParent string) {
	t.Helper()
	entries, err := os.ReadDir(workspaceParent)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func requireDashboardCleanup(t *testing.T, dashboard *Dashboard) {
	t.Helper()
	select {
	case <-dashboard.cleanupDone:
	case <-time.After(15 * time.Second):
		t.Fatal("dashboard cleanup did not complete")
	}
}
