//go:build linux

package scenario

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

const reconnectScenarioName = "reconnect"

var ErrReconnectDashboardExitFault = errors.New("reconnect scenario: injected Dashboard exit")

func (Reconnect) RunWithEvidence(ctx context.Context, input ReconnectInput) (result Result, reconnectEvidence ReconnectEvidence, runErr error) {
	assertions := NewAssertionSet()
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: input.Paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	dashboardRoot := dashboardInstance.WorkspaceRoot()
	var agentInstance *agent.Agent
	var agentRoot string
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		cleanupErr := stopTransferProcesses(cleanupContext, agentInstance, dashboardInstance)
		reconnectEvidence.AgentCleanup = agentCleanupReceipt(agentInstance)
		reconnectEvidence.DashboardCleanup = dashboardInstance.CleanupReceipt()
		cleanupErr = errors.Join(cleanupErr, transferWorkspaceResidue(agentRoot, dashboardRoot))
		assertions.Record("multi-generation process listener and workspace cleanup completed", cleanupErr == nil, errorText(cleanupErr))
		result.CleanupOK = cleanupErr == nil
		if cleanupErr != nil {
			result, reconnectEvidence, runErr = reconnectFinish(assertions, errors.Join(runErr, cleanupErr), reconnectEvidence)
			result.CleanupOK = false
			return
		}
		result.Assertions = assertions.Results()
	}()

	const agentUUID = "00000000-0000-0000-0000-000000000217"
	agentInstance, err = agent.Start(ctx, agent.AgentStartConfig{SourceDir: input.Paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(), Secret: dashboardInstance.AgentSecret(), UUID: agentUUID})
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	agentRoot = agentInstance.WorkspaceRoot()
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	readiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	serverID, err := transferServerID(ctx, dashboardInstance.Clients().MCP, readiness.UUID)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	fixturePath, sentinelPath, err := prepareReconnectSentinel(agentRoot)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	sentinelBytes, err := os.ReadFile(sentinelPath)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	dashboardConfig, err := os.ReadFile(dashboardInstance.ConfigPath())
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	agentConfig, err := os.ReadFile(agentInstance.ConfigPath())
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	fixtureBefore := dashboardInstance.FixtureIdentity()
	runtimeBefore := dashboardInstance.RuntimeIdentity()
	agentBefore := agentInstance.RuntimeIdentity()
	clientsBefore := dashboardInstance.Clients()
	bootstrapBefore := dashboardInstance.Bootstrap()
	reconnectEvidence.Fixture = ReconnectFixtureEvidence{Dashboard: fixtureBefore, AgentRoot: agentRoot, AgentConfigPath: agentInstance.ConfigPath(), AgentBinaryPath: agentInstance.BinaryPath()}
	reconnectEvidence.Runtime.DashboardBefore = runtimeBefore
	reconnectEvidence.Runtime.AgentBefore = agentBefore
	reconnectEvidence.Identity = ReconnectIdentityEvidence{ServerID: serverID, UUID: agentUUID}

	stoppedRuntime, err := dashboardInstance.StopProcess(ctx)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	disconnectAt := time.Now().UTC()
	assertions.Record("Dashboard disconnect barrier stopped generation one", stoppedRuntime == runtimeBefore && dashboardInstance.RuntimeIdentity().PID == 0, "")
	if input.DashboardFault == "dashboard-exit" {
		// This fault returns before the normal lifecycle evidence finalization below.
		sentinelAfter, sentinelErr := os.ReadFile(sentinelPath)
		sentinelUnchanged := sentinelErr == nil && bytes.Equal(sentinelBytes, sentinelAfter)
		reconnectEvidence.Lifecycle.DisconnectAt = disconnectAt
		reconnectEvidence.Lifecycle.OutsideRootSentinelUnchanged = sentinelUnchanged
		assertions.Record("outside-root sentinel remains unchanged", sentinelUnchanged, errorText(sentinelErr))
		if sentinelErr != nil {
			return reconnectFinish(assertions, errors.Join(ErrReconnectDashboardExitFault, sentinelErr), reconnectEvidence)
		}
		if !sentinelUnchanged {
			return reconnectFinish(assertions, errors.Join(ErrReconnectDashboardExitFault, errors.New("outside-root sentinel changed")), reconnectEvidence)
		}
		return reconnectFinish(assertions, ErrReconnectDashboardExitFault, reconnectEvidence)
	}
	runtimeAfter, err := dashboardInstance.StartProcess(ctx)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	postDashboardReadiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	reconnectAt := time.Now().UTC()
	serverIDAfter, err := transferServerID(ctx, dashboardInstance.Clients().MCP, postDashboardReadiness.UUID)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	dashboardConfigAfter, err := os.ReadFile(dashboardInstance.ConfigPath())
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	fixtureAfter := dashboardInstance.FixtureIdentity()
	clientsAfter := dashboardInstance.Clients()
	bootstrapAfter := dashboardInstance.Bootstrap()
	reconnectEvidence.Runtime.DashboardAfter = runtimeAfter
	reconnectEvidence.Identity.DashboardConfigUnchanged = bytes.Equal(dashboardConfig, dashboardConfigAfter)
	reconnectEvidence.Identity.DashboardFixtureUnchanged = fixtureAfter == fixtureBefore
	reconnectEvidence.Identity.ClientsRecreated = clientsAfter.REST != clientsBefore.REST && clientsAfter.MCP != clientsBefore.MCP && clientsAfter.WebSocket != clientsBefore.WebSocket
	reconnectEvidence.Identity.BootstrapRecreated = bootstrapAfter.PATID != 0 && bootstrapAfter.PATID != bootstrapBefore.PATID && bootstrapAfter.LoginAuthenticated && bootstrapAfter.MCPToolCount > 0
	assertions.Record("Dashboard generation two preserves fixture and recreates runtime clients", runtimeAfter.Generation > runtimeBefore.Generation && runtimeAfter.PID != runtimeBefore.PID && reconnectEvidence.Identity.DashboardFixtureUnchanged && reconnectEvidence.Identity.ClientsRecreated && reconnectEvidence.Identity.BootstrapRecreated, "")
	assertions.Record("Agent reconnect preserves exact server ID and UUID", serverIDAfter == serverID && postDashboardReadiness.UUID == agentUUID, "")

	dashboardPairs, err := runDashboardReconnectOperations(ctx, dashboardInstance, serverID, agentUUID, fixturePath)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	stateGenerationBefore := dashboardInstance.StateGeneration(serverID, agentUUID)
	transition, err := agentInstance.RestartProcess(ctx)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	if err := dashboardInstance.WaitForStateGeneration(ctx, serverID, agentUUID, stateGenerationBefore+1, 1); err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	postAgentReadiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	agentConfigAfter, err := os.ReadFile(agentInstance.ConfigPath())
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	agentPairs, err := runAgentRestartOperations(ctx, dashboardInstance, serverID, fixturePath)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	taskIDs, resultIDs, duplicates, lost := reconnectReceiptSummary(dashboardPairs, agentPairs)
	stale := staleReconnectReceiptCount(runtimeBefore.Generation, dashboardPairs, agentPairs)
	sentinelAfter, err := os.ReadFile(sentinelPath)
	if err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	reconnectEvidence.Runtime.AgentAfter = transition.Current
	reconnectEvidence.Runtime.StateGenerationBeforeAgentRestart = stateGenerationBefore
	reconnectEvidence.Runtime.StateGenerationAfterAgentRestart = dashboardInstance.StateGeneration(serverID, agentUUID)
	reconnectEvidence.Identity.AgentConfigUnchanged = bytes.Equal(agentConfig, agentConfigAfter) && agentInstance.ConfigPath() == reconnectEvidence.Fixture.AgentConfigPath && agentInstance.BinaryPath() == reconnectEvidence.Fixture.AgentBinaryPath && agentInstance.WorkspaceRoot() == reconnectEvidence.Fixture.AgentRoot
	reconnectEvidence.Lifecycle = ReconnectLifecycleEvidence{DisconnectAt: disconnectAt, ReconnectAt: reconnectAt, ReconnectInterval: reconnectAt.Sub(disconnectAt), DashboardReceipts: dashboardPairs, AgentReceipts: agentPairs, StaleGenerationReceipts: stale, DuplicateTaskIDs: duplicates, LostResultIDs: lost, OutsideRootSentinelUnchanged: bytes.Equal(sentinelBytes, sentinelAfter)}
	reconnectEvidence.Observation = ReconnectObservation{ServerID: serverID, UUID: agentUUID, OldGeneration: runtimeBefore.Generation, NewGeneration: runtimeAfter.Generation, DisconnectAt: disconnectAt, ReconnectAt: reconnectAt, TaskIDs: taskIDs, ResultIDs: resultIDs, PostReconnect: true, AgentRestarted: transition.Previous == agentBefore && transition.Current.Generation > transition.Previous.Generation && postAgentReadiness.UUID == agentUUID}
	assertions.Record("post-reconnect MCP task and result receipts are exactly once", duplicates == 0 && lost == 0 && len(taskIDs) == 5, "")
	assertions.Record("stale Dashboard generation cannot receive new task receipts", stale == 0, "")
	assertions.Record("Agent restart advances state stream and preserves config identity", reconnectEvidence.Runtime.StateGenerationAfterAgentRestart > stateGenerationBefore && reconnectEvidence.Identity.AgentConfigUnchanged && reconnectEvidence.Observation.AgentRestarted, "")
	assertions.Record("outside-root sentinel remains unchanged", reconnectEvidence.Lifecycle.OutsideRootSentinelUnchanged, "")
	if err := reconnectEvidence.Validate(); err != nil {
		return reconnectFinish(assertions, err, reconnectEvidence)
	}
	return reconnectFinish(assertions, nil, reconnectEvidence)
}

func agentCleanupReceipt(agentInstance *agent.Agent) processharness.CleanupReceipt {
	if agentInstance == nil {
		return processharness.CleanupReceipt{}
	}
	return agentInstance.CleanupReceipt()
}

func reconnectFinish(assertions *AssertionSet, runErr error, reconnectEvidence ReconnectEvidence) (Result, ReconnectEvidence, error) {
	for _, assertion := range assertions.assertions {
		if !assertion.Passed && runErr == nil {
			runErr = errors.New(assertion.Name + ": " + assertion.Details)
		}
	}
	result := Result{Name: reconnectScenarioName, Passed: runErr == nil, Assertions: assertions.Results()}
	if runErr != nil {
		result.Error = errorText(runErr)
	}
	return result, reconnectEvidence, runErr
}
