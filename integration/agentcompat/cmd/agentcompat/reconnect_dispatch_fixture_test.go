package main

import (
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/scenario"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

func completeReconnectDispatchEvidence(t *testing.T) scenario.ReconnectEvidence {
	t.Helper()
	disconnectAt := time.Date(2026, 2, 3, 4, 5, 6, 700, time.UTC)
	reconnectAt := disconnectAt.Add(7 * time.Second)
	dashboardReceipt := dashboard.MCPReceiptPair{
		Task:   dashboard.MCPReceiptEvent{Sequence: 51, DashboardGeneration: 12, GateGeneration: 61, ServerID: 41, TaskID: 71, TaskType: 81, Kind: dashboard.MCPReceiptTask},
		Result: dashboard.MCPReceiptEvent{Sequence: 52, DashboardGeneration: 12, GateGeneration: 61, ServerID: 41, TaskID: 71, TaskType: 81, Kind: dashboard.MCPReceiptResult},
	}
	agentReceipt := dashboard.MCPReceiptPair{
		Task:   dashboard.MCPReceiptEvent{Sequence: 53, DashboardGeneration: 12, GateGeneration: 62, ServerID: 41, TaskID: 72, TaskType: 82, Kind: dashboard.MCPReceiptTask},
		Result: dashboard.MCPReceiptEvent{Sequence: 54, DashboardGeneration: 12, GateGeneration: 62, ServerID: 41, TaskID: 72, TaskType: 82, Kind: dashboard.MCPReceiptResult},
	}
	evidence := scenario.ReconnectEvidence{
		Fixture: scenario.ReconnectFixtureEvidence{
			Dashboard: dashboard.FixtureIdentity{
				WorkspaceRoot: "/typed/dashboard-workspace", ConfigPath: "/typed/dashboard.yaml", DatabasePath: "/typed/dashboard.sqlite", BinaryPath: "/typed/dashboard",
				HTTP: workspace.ListenerIdentity{Address: "127.0.0.1:41001", Inode: 1101}, Receipt: workspace.ListenerIdentity{Address: "127.0.0.1:41002", Inode: 1102}, HTTPS: workspace.ListenerIdentity{Address: "127.0.0.1:41003", Inode: 1103},
			},
			AgentRoot: "/typed/agent-workspace", AgentConfigPath: "/typed/agent.yaml", AgentBinaryPath: "/typed/agent",
		},
		Runtime: scenario.ReconnectRuntimeEvidence{
			DashboardBefore: dashboard.RuntimeIdentity{Generation: 11, PID: 2101, ProcessGroupID: 3101}, DashboardAfter: dashboard.RuntimeIdentity{Generation: 12, PID: 2102, ProcessGroupID: 3102},
			AgentBefore: agent.ProcessIdentity{Generation: 21, PID: 2201, ProcessGroupID: 3201}, AgentAfter: agent.ProcessIdentity{Generation: 22, PID: 2202, ProcessGroupID: 3202},
			StateGenerationBeforeAgentRestart: 31, StateGenerationAfterAgentRestart: 32,
		},
		Identity: scenario.ReconnectIdentityEvidence{ServerID: 41, UUID: "00000000-0000-0000-0000-000000000041", DashboardConfigUnchanged: true, AgentConfigUnchanged: true, DashboardFixtureUnchanged: true, ClientsRecreated: true, BootstrapRecreated: true},
		Lifecycle: scenario.ReconnectLifecycleEvidence{
			DisconnectAt: disconnectAt, ReconnectAt: reconnectAt, ReconnectInterval: 7 * time.Second,
			DashboardReceipts: []dashboard.MCPReceiptPair{dashboardReceipt}, AgentReceipts: []dashboard.MCPReceiptPair{agentReceipt},
			StaleGenerationReceipts: 0, DuplicateTaskIDs: 0, LostResultIDs: 0, OutsideRootSentinelUnchanged: true,
		},
		Observation:      scenario.ReconnectObservation{ServerID: 41, UUID: "00000000-0000-0000-0000-000000000041", OldGeneration: 11, NewGeneration: 12, DisconnectAt: disconnectAt, ReconnectAt: reconnectAt, TaskIDs: []uint64{71, 72}, ResultIDs: []uint64{71, 72}, PostReconnect: true, AgentRestarted: true},
		AgentCleanup:     processharness.CleanupReceipt{Passed: true, Forced: false, Processes: []processharness.CleanupRecord{{Name: "agent-generation-21", PID: 2201, Forced: false, Error: ""}, {Name: "agent-generation-22", PID: 2202, Forced: false, Error: ""}}},
		DashboardCleanup: processharness.CleanupReceipt{Passed: true, Forced: false, Processes: []processharness.CleanupRecord{{Name: "dashboard-generation-11", PID: 2101, Forced: false, Error: ""}, {Name: "dashboard-generation-12", PID: 2102, Forced: false, Error: ""}}},
	}
	assertCompleteReconnectDispatchEvidence(t, evidence)
	return evidence
}

func assertCompleteReconnectDispatchEvidence(t *testing.T, evidence scenario.ReconnectEvidence) {
	t.Helper()
	if err := evidence.Validate(); err != nil {
		t.Fatalf("incomplete reconnect dispatch fixture: %v", err)
	}
	fixture := evidence.Fixture
	if fixture.Dashboard.WorkspaceRoot == "" || fixture.Dashboard.ConfigPath == "" || fixture.Dashboard.DatabasePath == "" || fixture.Dashboard.BinaryPath == "" || fixture.AgentRoot == "" || fixture.AgentConfigPath == "" || fixture.AgentBinaryPath == "" {
		t.Fatal("incomplete reconnect dispatch fixture paths")
	}
	for name, listener := range map[string]struct {
		address string
		inode   uint64
	}{
		"http":    {fixture.Dashboard.HTTP.Address, fixture.Dashboard.HTTP.Inode},
		"receipt": {fixture.Dashboard.Receipt.Address, fixture.Dashboard.Receipt.Inode},
		"https":   {fixture.Dashboard.HTTPS.Address, fixture.Dashboard.HTTPS.Inode},
	} {
		if listener.address == "" || listener.inode == 0 {
			t.Fatalf("incomplete reconnect dispatch %s listener", name)
		}
	}
	if len(evidence.Lifecycle.DashboardReceipts) == 0 || len(evidence.Lifecycle.AgentReceipts) == 0 {
		t.Fatal("incomplete reconnect dispatch receipt pairs")
	}
	for _, pairs := range [][]dashboard.MCPReceiptPair{evidence.Lifecycle.DashboardReceipts, evidence.Lifecycle.AgentReceipts} {
		for _, pair := range pairs {
			if pair.Task.Sequence == 0 || pair.Task.DashboardGeneration == 0 || pair.Task.GateGeneration == 0 || pair.Task.ServerID == 0 || pair.Task.TaskID == 0 || pair.Task.TaskType == 0 || pair.Task.Kind != dashboard.MCPReceiptTask {
				t.Fatalf("incomplete reconnect dispatch task receipt: %#v", pair.Task)
			}
			if pair.Result.Sequence == 0 || pair.Result.DashboardGeneration == 0 || pair.Result.GateGeneration == 0 || pair.Result.ServerID == 0 || pair.Result.TaskID == 0 || pair.Result.TaskType == 0 || pair.Result.Kind != dashboard.MCPReceiptResult {
				t.Fatalf("incomplete reconnect dispatch result receipt: %#v", pair.Result)
			}
		}
	}
	assertCompleteCleanupReceipt(t, "agent", evidence.AgentCleanup)
	assertCompleteCleanupReceipt(t, "dashboard", evidence.DashboardCleanup)
}

func assertCompleteCleanupReceipt(t *testing.T, name string, receipt processharness.CleanupReceipt) {
	t.Helper()
	if !receipt.Passed || receipt.Forced || len(receipt.Processes) == 0 {
		t.Fatalf("incomplete reconnect dispatch %s cleanup receipt: %#v", name, receipt)
	}
	for _, record := range receipt.Processes {
		if record.Name == "" || record.PID == 0 || record.Forced || record.Error != "" {
			t.Fatalf("incomplete reconnect dispatch %s cleanup record: %#v", name, record)
		}
	}
}
