package evidence

import (
	"fmt"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type ProfileMetadata struct {
	Name                      string `json:"name"`
	JobTimeoutSeconds         int64  `json:"job_timeout_seconds"`
	SuiteDeadlineSeconds      int64  `json:"suite_deadline_seconds"`
	DefaultSeed               string `json:"default_seed"`
	AgentCount                int    `json:"agent_count"`
	StressRounds              int    `json:"stress_rounds"`
	ConcurrentOperations      int    `json:"concurrent_operations"`
	ConcurrentSessionsPerKind int    `json:"concurrent_sessions_per_kind"`
	TransferPairs             int    `json:"transfer_pairs"`
	TransferBytes             uint64 `json:"transfer_bytes"`
	DashboardRestartCycles    int    `json:"dashboard_restart_cycles"`
	Iterations                int    `json:"iterations"`
	StreamBoundaryAllowed     int    `json:"stream_boundary_allowed"`
	StreamBoundaryRejected    int    `json:"stream_boundary_rejected"`
}

type ResourceBudgetMetadata struct {
	WarmupRunsPerPath          int    `json:"warmup_runs_per_path"`
	BaselineSampleCount        int    `json:"baseline_sample_count"`
	EndSampleCount             int    `json:"end_sample_count"`
	SampleIntervalMilliseconds int64  `json:"sample_interval_milliseconds"`
	ChildProcessCountDrift     int    `json:"child_process_count_drift"`
	ListenerCountDrift         int    `json:"listener_count_drift"`
	NonStdioFDCountDrift       int    `json:"non_stdio_fd_count_drift"`
	DashboardRSSDeltaBytes     uint64 `json:"dashboard_rss_delta_bytes"`
	AgentRSSDeltaBytes         uint64 `json:"agent_rss_delta_bytes"`
	TransferHeapBytes          uint64 `json:"transfer_heap_bytes"`
}

func profileMetadata(profile contract.Profile) ProfileMetadata {
	return ProfileMetadata{Name: string(profile.Name()), JobTimeoutSeconds: int64(profile.JobTimeout().Seconds()), SuiteDeadlineSeconds: int64(profile.SuiteDeadline().Seconds()), DefaultSeed: fmt.Sprintf("0x%x", uint64(profile.Seed())), AgentCount: profile.AgentCount(), StressRounds: profile.StressRounds(), ConcurrentOperations: profile.ConcurrentOperations(), ConcurrentSessionsPerKind: profile.ConcurrentSessions(), TransferPairs: profile.TransferPairs(), TransferBytes: profile.TransferBytes(), DashboardRestartCycles: profile.DashboardRestartCycles(), Iterations: profile.Iterations(), StreamBoundaryAllowed: profile.StreamBoundaryAllowed(), StreamBoundaryRejected: profile.StreamBoundaryRejected()}
}

func resourceBudgetMetadata(budget contract.ResourceBudget) ResourceBudgetMetadata {
	return ResourceBudgetMetadata{WarmupRunsPerPath: budget.WarmupRuns(), BaselineSampleCount: budget.SampleCount(), EndSampleCount: budget.SampleCount(), SampleIntervalMilliseconds: budget.SampleInterval().Milliseconds(), ChildProcessCountDrift: budget.ChildProcessCountDrift(), ListenerCountDrift: budget.ListenerCountDrift(), NonStdioFDCountDrift: budget.NonStdioFDCountDrift(), DashboardRSSDeltaBytes: budget.DashboardRSSDeltaBytes(), AgentRSSDeltaBytes: budget.AgentRSSDeltaBytes(), TransferHeapBytes: budget.TransferHeapBytes()}
}

func (p ProfileMetadata) Validate() error {
	if p.Name == "" || p.JobTimeoutSeconds < 1 || p.SuiteDeadlineSeconds < 1 || p.DefaultSeed == "" || p.DefaultSeed == "0x0" || p.AgentCount < 1 || p.StressRounds < 1 || p.ConcurrentOperations < 1 || p.ConcurrentSessionsPerKind < 1 || p.TransferPairs < 1 || p.TransferBytes == 0 || p.DashboardRestartCycles < 1 || p.Iterations < 1 || p.StreamBoundaryAllowed < 1 || p.StreamBoundaryRejected <= p.StreamBoundaryAllowed {
		return fmt.Errorf("profile fields are invalid")
	}
	return nil
}

func (b ResourceBudgetMetadata) Validate() error {
	if b.WarmupRunsPerPath < 1 || b.BaselineSampleCount < 1 || b.EndSampleCount < 1 || b.SampleIntervalMilliseconds < 1 || b.ChildProcessCountDrift < 0 || b.ListenerCountDrift < 0 || b.NonStdioFDCountDrift < 0 || b.DashboardRSSDeltaBytes == 0 || b.AgentRSSDeltaBytes == 0 || b.TransferHeapBytes == 0 {
		return fmt.Errorf("resource budget fields are invalid")
	}
	return nil
}
