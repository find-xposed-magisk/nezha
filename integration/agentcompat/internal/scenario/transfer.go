//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

const transferScenarioName = "transfer-100mib"

var ErrTransferHashFault = errors.New("transfer scenario: injected hash mismatch")

type TransferInput struct {
	Paths contract.Paths
	Fault contract.Fault
}

type Transfer struct{}

func (scenario Transfer) Run(ctx context.Context, input TransferInput) (Result, error) {
	result, _, err := scenario.RunWithEvidence(ctx, input)
	return result, err
}

func (Transfer) RunWithEvidence(ctx context.Context, input TransferInput) (result Result, transferEvidence TransferEvidence, runErr error) {
	assertions := NewAssertionSet()
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: input.Paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	var agentInstance *agent.Agent
	var agentWorkspaceRoot string
	dashboardWorkspaceRoot := dashboardInstance.WorkspaceRoot()
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		cleanupErr := stopTransferProcesses(cleanupContext, agentInstance, dashboardInstance)
		residueErr := transferWorkspaceResidue(agentWorkspaceRoot, dashboardWorkspaceRoot)
		cleanupErr = errors.Join(cleanupErr, residueErr)
		assertions.Record("process listener and workspace cleanup completed", cleanupErr == nil, errorText(cleanupErr))
		result.CleanupOK = cleanupErr == nil
		if cleanupErr != nil {
			result, runErr = transferFinish(assertions, errors.Join(runErr, cleanupErr))
			result.CleanupOK = false
		} else {
			result.Assertions = assertions.Results()
		}
	}()

	agentInstance, err = agent.Start(ctx, agent.AgentStartConfig{
		SourceDir: input.Paths.AgentSource().String(),
		Endpoint:  dashboardInstance.Endpoint(),
		Secret:    dashboardInstance.AgentSecret(),
		UUID:      "00000000-0000-0000-0000-000000000216",
	})
	if err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	agentWorkspaceRoot = agentInstance.WorkspaceRoot()
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	readiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	if err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	serverID, err := transferServerID(ctx, dashboardInstance.Clients().MCP, readiness.UUID)
	if err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	root, err := fixture.NewAgentRoot(agentInstance.WorkspaceRoot(), "transfer-files")
	if err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	sentinels, err := newTransferSentinels(root, agentInstance.WorkspaceRoot())
	if err != nil {
		result, runErr = transferFinish(assertions, err)
		return result, transferEvidence, runErr
	}
	defer sentinels.close()
	execution := transferExecution{
		client:       dashboardInstance.Clients().MCP,
		serverID:     serverID,
		root:         root,
		residueScope: transferResidueScope{AgentRoot: agentInstance.WorkspaceRoot(), DashboardPID: dashboardInstance.PID()},
		sentinels:    sentinels,
	}
	transferEvidence, err = execution.run(ctx, assertions, input.Fault)
	result, runErr = transferFinish(assertions, err)
	return result, transferEvidence, runErr
}

func transferWorkspaceResidue(workspaceRoots ...string) error {
	var residueErr error
	for _, workspaceRoot := range workspaceRoots {
		if workspaceRoot == "" {
			continue
		}
		if _, err := os.Stat(workspaceRoot); err == nil {
			residueErr = errors.Join(residueErr, fmt.Errorf("workspace remains: %s", workspaceRoot))
		} else if !errors.Is(err, os.ErrNotExist) {
			residueErr = errors.Join(residueErr, fmt.Errorf("inspect workspace %s: %w", workspaceRoot, err))
		}
	}
	return residueErr
}

func stopTransferProcesses(ctx context.Context, agentInstance *agent.Agent, dashboardInstance *dashboard.Dashboard) error {
	var cleanupErr error
	if agentInstance != nil {
		stopErr := agentInstance.Stop(ctx)
		receipt := agentInstance.CleanupReceipt()
		if stopErr != nil || !receipt.Passed || receipt.Forced {
			cleanupErr = errors.Join(cleanupErr, stopErr, errors.New("agent cleanup receipt failed"))
		}
	}
	stopErr := dashboardInstance.Stop(ctx)
	receipt := dashboardInstance.CleanupReceipt()
	if stopErr != nil || !receipt.Passed || receipt.Forced {
		cleanupErr = errors.Join(cleanupErr, stopErr, errors.New("dashboard cleanup receipt failed"))
	}
	return cleanupErr
}

func transferFinish(assertions *AssertionSet, runErr error) (Result, error) {
	for _, assertion := range assertions.assertions {
		if !assertion.Passed && runErr == nil {
			runErr = errors.New(assertion.Name + ": " + assertion.Details)
		}
	}
	result := Result{Name: transferScenarioName, Passed: runErr == nil, Assertions: assertions.Results()}
	if runErr != nil {
		result.Error = errorText(runErr)
	}
	return result, runErr
}

func transferServerID(ctx context.Context, mcpClient *client.Client, uuid string) (uint64, error) {
	servers, err := client.CallTool[client.ServerListArguments, client.ServerListResult](ctx, mcpClient, client.ToolCall[client.ServerListArguments]{
		Name: "server.list", Arguments: client.ServerListArguments{OnlineOnly: true},
	})
	if err != nil {
		return 0, fmt.Errorf("list transfer Agents: %w", err)
	}
	for _, server := range servers.StructuredContent.Servers {
		if server.UUID == uuid && server.Online {
			return server.ID, nil
		}
	}
	return 0, errors.New("online transfer Agent not found")
}
