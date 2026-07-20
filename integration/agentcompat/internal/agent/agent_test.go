//go:build linux && agentcompat

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/testpaths"
)

func TestAgent_BecomesOnlineOverH2C(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboardWithReceiptGate(t)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{UUID: "00000000-0000-0000-0000-000000000081"})
	receiptAccepted := make(chan error, 1)
	go func() { receiptAccepted <- dashboardInstance.WaitForReceiptAccepted(t.Context()) }()
	select {
	case err := <-receiptAccepted:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for withheld state receipt")
	}
	serverBeforeRelease := requireOnlineServer(t, dashboardInstance, agentInstance.UUID())
	require.NotZero(t, serverBeforeRelease.LastActive)
	stateGeneration := dashboardInstance.StateGeneration(serverBeforeRelease.ID, agentInstance.UUID())
	require.NotZero(t, stateGeneration)
	require.NoError(t, dashboardInstance.WaitForStateGeneration(t.Context(), serverBeforeRelease.ID, agentInstance.UUID(), stateGeneration, 1))
	stateTwoBeforeRelease, cancelStateTwo := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	require.ErrorIs(t, dashboardInstance.WaitForStateGeneration(stateTwoBeforeRelease, serverBeforeRelease.ID, agentInstance.UUID(), stateGeneration, 2), context.DeadlineExceeded)
	cancelStateTwo()
	require.Equal(t, uint64(1), dashboardInstance.ReceiptAcceptedCount())
	require.NoError(t, dashboardInstance.ReleaseReceipt(t.Context()))
	secondState := make(chan error, 1)
	go func() { secondState <- dashboardInstance.WaitForSecondState(t.Context()) }()

	// When
	readiness, err := agentInstance.WaitReady(t.Context(), dashboardInstance)

	// Then
	require.NoError(t, err)
	require.NotZero(t, readiness.ServerID)
	require.Equal(t, serverBeforeRelease.ID, readiness.ServerID)
	require.Equal(t, agentInstance.UUID(), readiness.UUID)
	require.Equal(t, "v2.1.0", readiness.Version)
	require.True(t, readiness.VersionObserved)
	require.True(t, readiness.RequestTaskEstablished)
	require.True(t, readiness.StateReceiptObserved)
	require.NoError(t, <-secondState)
	require.Equal(t, uint64(2), dashboardInstance.ReceiptAcceptedCount())
	require.NoError(t, dashboardInstance.WaitForInfo2(t.Context(), serverBeforeRelease.ID, agentInstance.UUID()))
	require.NotNil(t, readiness.Host)
	require.NotNil(t, readiness.State)
	require.True(t, readiness.Online)
}

func TestAgent_BecomesOnlineOverVerifiedTLS(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, true)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{
		UUID:       "00000000-0000-0000-0000-000000000082",
		TLS:        true,
		CAFilePath: dashboardInstance.TLSCACertificatePath(),
	})

	// When
	readiness, err := agentInstance.WaitReady(t.Context(), dashboardInstance)

	// Then
	require.NoError(t, err)
	require.True(t, readiness.Online)
	require.NotNil(t, readiness.Host)
	require.NotNil(t, readiness.State)
	require.WithinDuration(t, time.Now(), readiness.LastActive, 30*time.Second)
	var host struct {
		Platform string `json:"platform"`
		Version  string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(readiness.Host, &host))
	require.Equal(t, "v2.1.0", host.Version)
	require.NotEmpty(t, host.Platform)
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(readiness.State, &state))
	require.Contains(t, state, "uptime")
}

func TestAgent_RejectsUnknownTLSAuthority(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, true)
	wrongCAPath := filepath.Join(t.TempDir(), "wrong-ca.crt")
	wrongFixture, fixtureErr := fixture.NewLocalTLSFixture(time.Now())
	require.NoError(t, fixtureErr)
	require.NoError(t, os.WriteFile(wrongCAPath, wrongFixture.CAPEM(), 0o600))
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{
		UUID:       "00000000-0000-0000-0000-000000000086",
		TLS:        true,
		Debug:      true,
		CAFilePath: wrongCAPath,
	})

	// When
	readinessContext, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	_, err := agentInstance.WaitReady(readinessContext, dashboardInstance)

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	logData, logErr := os.ReadFile(agentInstance.LogPath())
	require.NoError(t, logErr)
	require.Contains(t, string(logData), "x509: certificate signed by unknown authority")
	config, readErr := os.ReadFile(agentInstance.ConfigPath())
	require.NoError(t, readErr)
	stopContext, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	require.NoError(t, agentInstance.Stop(stopContext))
	stopCancel()
	require.Contains(t, string(config), "insecure_tls: false")
	require.NotContains(t, string(config), "insecure_tls: true")
}

func TestAgent_AssertNeverOnlineFailsClosedWhenDashboardUnavailable(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, false)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{
		UUID:   "00000000-0000-0000-0000-000000000087",
		Secret: "wrong-agent-secret",
	})
	require.NoError(t, dashboardInstance.Stop(context.Background()))

	// When
	err := agentInstance.AssertNeverOnline(t.Context(), dashboardInstance, time.Second)

	// Then
	require.Error(t, err)
}

func TestAgent_RejectsInvalidSecret(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, false)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{
		UUID:   "00000000-0000-0000-0000-000000000083",
		Secret: "wrong-agent-secret",
	})

	// When
	err := agentInstance.AssertNeverOnline(t.Context(), dashboardInstance, 2*time.Second)

	// Then
	require.NoError(t, err)
	stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, agentInstance.Stop(stopContext))
}

func TestAgent_StopsCleanly(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, false)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{UUID: "00000000-0000-0000-0000-000000000084"})
	require.NoError(t, waitForAgentReady(t, agentInstance, dashboardInstance))

	// When
	stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := agentInstance.Stop(stopContext)

	// Then
	require.NoError(t, err)
	require.True(t, agentInstance.CleanupReceipt().Passed)
	require.False(t, agentInstance.CleanupReceipt().Forced)
}

func TestAgent_StopRemovesAgentFromOnlineOnlyList(t *testing.T) {
	// Given
	dashboardInstance := startTestDashboard(t, false)
	agentInstance := startTestAgent(t, dashboardInstance, AgentStartConfig{UUID: "00000000-0000-0000-0000-000000000088"})
	require.NoError(t, waitForAgentReady(t, agentInstance, dashboardInstance))
	stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, agentInstance.Stop(stopContext))

	// When
	list, err := client.CallTool[serverListArguments, serverListResult](
		t.Context(), dashboardInstance.Clients().MCP,
		client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}},
	)

	// Then
	require.NoError(t, err)
	foundOnline := false
	for _, server := range list.StructuredContent.Servers {
		if server.UUID == agentInstance.UUID() {
			foundOnline = true
		}
	}
	require.False(t, foundOnline)
}

func startTestDashboard(t *testing.T, enableTLS bool) *dashboard.Dashboard {
	t.Helper()
	sourceDir, err := testpaths.NezhaSource(t.Name())
	require.NoError(t, err)
	instance, err := dashboard.Start(t.Context(), dashboard.StartConfig{SourceDir: sourceDir, EnableTLS: enableTLS, ReadinessTimeout: readinessBudget})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, instance.Stop(cleanupContext))
	})
	return instance
}

func startTestDashboardWithReceiptGate(t *testing.T) *dashboard.Dashboard {
	t.Helper()
	sourceDir, err := testpaths.NezhaSource(t.Name())
	require.NoError(t, err)
	instance, err := dashboard.Start(t.Context(), dashboard.StartConfig{SourceDir: sourceDir, ReceiptGate: true, ReadinessTimeout: readinessBudget})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, instance.Stop(cleanupContext))
	})
	return instance
}

func startTestAgent(t *testing.T, dashboardInstance *dashboard.Dashboard, config AgentStartConfig) *Agent {
	t.Helper()
	if config.Secret == "" {
		config.Secret = dashboardInstance.AgentSecret()
	}
	if config.TLS {
		config.Endpoint = dashboardInstance.TLSEndpoint()
	} else {
		config.Endpoint = dashboardInstance.Endpoint()
	}
	instance, err := Start(t.Context(), AgentStartConfig{
		SourceDir:  testAgentSourceDir(t),
		Endpoint:   config.Endpoint,
		Secret:     config.Secret,
		UUID:       config.UUID,
		TLS:        config.TLS,
		Debug:      config.Debug,
		CAFilePath: config.CAFilePath,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, instance.Stop(cleanupContext))
	})
	return instance
}

func testAgentSourceDir(t *testing.T) string {
	t.Helper()
	if sourceDir := os.Getenv("AGENT_SOURCE"); sourceDir != "" {
		return sourceDir
	}
	nezhaSource, err := testpaths.NezhaSource(t.Name())
	require.NoError(t, err)
	agentSourceDir, err := testpaths.AgentSource(nezhaSource)
	require.NoError(t, err)
	return agentSourceDir
}

func requireOnlineServer(t *testing.T, dashboardInstance *dashboard.Dashboard, uuid string) serverListItem {
	t.Helper()
	list, err := client.CallTool[serverListArguments, serverListResult](t.Context(), dashboardInstance.Clients().MCP, client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}})
	require.NoError(t, err)
	for _, server := range list.StructuredContent.Servers {
		if server.UUID == uuid {
			return server
		}
	}
	t.Fatalf("server %q is not online", uuid)
	return serverListItem{}
}

func waitForAgentReady(t *testing.T, instance *Agent, dashboardInstance *dashboard.Dashboard) error {
	t.Helper()
	readinessContext, cancel := context.WithTimeout(t.Context(), readinessBudget)
	defer cancel()
	_, err := instance.WaitReady(readinessContext, dashboardInstance)
	return err
}
