//go:build linux && agentcompat

package scenario

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

func TestStressPRFullEightAgentExactlyOnce(t *testing.T) {
	requireHeldRealSources(t)
	paths, err := contract.NewPaths(os.Getenv("AGENTCOMPAT_NEZHA_SOURCE"), os.Getenv("AGENTCOMPAT_AGENT_SOURCE"), filepath.Join("/tmp", "nezha-agentcompat-real-stress"))
	require.NoError(t, err)
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), contract.PRFullSuiteDeadline)
	defer cancel()
	realFixture, err := startHeldSessionSetRealFixture(ctx, paths, plan)
	require.NoError(t, err)
	t.Cleanup(func() { _ = realFixture.close(context.Background(), nil) })
	input, err := realFixture.input(plan)
	require.NoError(t, err)
	set, err := NewHeldSessionSet(ctx, input)
	require.NoError(t, err)
	fdDiagnostics := newRealFDDiagnosticCollector(fdDiagnosticEnabled(os.Getenv("AGENTCOMPAT_FD_DIAGNOSTIC_TAIL")))
	defer fdDiagnostics.WaitAndLog(t)
	dashboardIdentity := realFixture.dashboard.RuntimeIdentity()
	agentIdentities := make([]agent.ProcessIdentity, len(realFixture.agents))
	workspaceRoots := make([]string, 0, len(realFixture.agents)+2)
	workspaceRoots = append(workspaceRoots, realFixture.dashboard.WorkspaceRoot(), realFixture.preparedBinary.WorkspaceRoot())
	for index, instance := range realFixture.agents {
		agentIdentities[index] = instance.RuntimeIdentity()
		workspaceRoots = append(workspaceRoots, instance.WorkspaceRoot())
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	warmups, warmupErr := runStressWarmups(ctx, realFixture, plan)
	require.NoError(t, warmupErr)
	require.NoError(t, drainStressDashboardSQLiteJournal(ctx, realFixture))
	baselineResources, resourceErr := captureStressResources(ctx, realFixture, stressResourceCaptureSpec{Phase: stressResourceBaseline, Diagnostics: fdDiagnostics})
	require.NoError(t, resourceErr)
	rounds := make([]StressRoundEvidence, 0, len(plan.Rounds))
	for _, round := range plan.Rounds {
		// WaitHealthy completes only after Close; live sessions are validated by NewHeldSessionSet.
		evidenceValue, roundErr := runStressRound(ctx, realFixture, round)
		require.NoError(t, roundErr)
		require.NoError(t, ValidateStressRoundEvidence(round, evidenceValue))
		rounds = append(rounds, evidenceValue)
	}
	quota, quotaErr := runRealStressQuotaProbe(ctx, realFixture, paths.AgentSource().String())
	require.NoError(t, quotaErr)
	endResources, resourceErr := captureStressResources(ctx, realFixture, stressResourceCaptureSpec{Phase: stressResourceEnd, Diagnostics: fdDiagnostics})
	require.NoError(t, resourceErr)
	fdDiagnostics.WaitAndLog(t)
	require.NoError(t, set.Close(ctx))
	require.NoError(t, set.WaitHealthy(ctx))
	cleanupErr := realFixture.close(ctx, nil)
	require.NoError(t, cleanupErr)
	cleanup := stressCleanupSummary(realFixture, dashboardIdentity, agentIdentities, workspaceRoots, cleanupErr)
	resourceWindows := make([]StressProcessWindows, len(baselineResources))
	for index := range baselineResources {
		resourceWindows[index] = StressProcessWindows{Process: baselineResources[index].Process, Baseline: baselineResources[index].Baseline, End: endResources[index].End}
	}
	evidenceValue := StressEvidence{Version: 1, Profile: plan.Profile, Seed: plan.Seed, PreparedBinaries: StressPreparedBinaries{DashboardBuildCount: 1, DashboardPathReused: true, AgentBuildCount: 1, AgentPathReused: true}, Quotas: quota, Warmups: warmups, Plan: plan, Iterations: []StressIterationEvidence{{Iteration: 1, Rounds: rounds, Resources: resourceWindows}}, Cleanup: cleanup}
	for _, session := range plan.Sessions {
		evidenceValue.Sessions = append(evidenceValue.Sessions, StressSessionEvidence{ID: session.ID, Kind: session.Kind, Succeeded: true})
	}
	require.NoError(t, publishStressEvidence("/tmp/nezha-held-real-sessions", evidenceValue))
	_, err = readStressEvidence("/tmp/nezha-held-real-sessions")
	require.NoError(t, err)
}

type stressResourceCapturePhase uint8

const (
	stressResourceBaseline stressResourceCapturePhase = iota
	stressResourceEnd
)

type stressResourceCaptureSpec struct {
	Phase       stressResourceCapturePhase
	Diagnostics *fdDiagnosticCollector
}

func TestStressResourceCaptureSpec_UsesDisabledDiagnosticsWhenNil(t *testing.T) {
	// Given / When
	diagnostics := (stressResourceCaptureSpec{}).diagnostics()

	// Then
	require.Nil(t, diagnostics)
	require.False(t, diagnostics.Enabled())
}

func (spec stressResourceCaptureSpec) diagnostics() *fdDiagnosticCollector {
	return spec.Diagnostics
}

func captureStressResources(ctx context.Context, fixture *heldSessionSetRealFixture, spec stressResourceCaptureSpec) ([]StressProcessWindows, error) {
	diagnostics := spec.diagnostics()
	result := make([]StressProcessWindows, 0, len(fixture.agents)+1)
	dashboard := fixture.dashboard.RuntimeIdentity()
	dashboardProcess, err := NewStressDashboardProcess(dashboard.PID)
	if err != nil {
		return nil, err
	}
	windowSpec := processharness.WindowSpec{PID: dashboard.PID, Interval: contract.ResourceSampleInterval}
	if spec.Phase == stressResourceBaseline {
		windowSpec.ObserveSample = observeStressDashboardSQLiteJournal(fixture.dashboard.DatabasePath() + "-journal")
	}
	baseline, err := processharness.SampleWindow(ctx, windowSpec)
	if err != nil {
		return nil, err
	}
	dashboardWindow := StressProcessWindows{Process: dashboardProcess}
	if spec.Phase == stressResourceEnd {
		dashboardWindow.End = baseline
	} else {
		dashboardWindow.Baseline = baseline
	}
	result = append(result, dashboardWindow)
	for index, instance := range fixture.agents {
		identity := instance.RuntimeIdentity()
		agentOrdinal, err := NewStressAgentOrdinal(index + 1)
		if err != nil {
			return nil, err
		}
		process, err := NewStressAgentProcess(agentOrdinal, identity.PID)
		if err != nil {
			return nil, err
		}
		baseline, err := processharness.SampleWindow(ctx, processharness.WindowSpec{PID: identity.PID, Interval: contract.ResourceSampleInterval, CaptureFDObservations: diagnostics.Enabled()})
		if err != nil {
			return nil, err
		}
		window := StressProcessWindows{Process: process}
		diagnosticWindow := fdDiagnosticAgentWindow{Process: process, Identity: identity, Window: baseline}
		if spec.Phase == stressResourceEnd {
			window.End = baseline
			diagnostics.RecordEnd(ctx, diagnosticWindow)
		} else {
			window.Baseline = baseline
			diagnostics.RecordBaseline(diagnosticWindow)
		}
		result = append(result, window)
	}
	return result, nil
}

type realStressQuotaResponse struct {
	UserAccepted   int  `json:"user_accepted"`
	UserRejected   int  `json:"user_rejected"`
	ServerAccepted int  `json:"server_accepted"`
	ServerRejected int  `json:"server_rejected"`
	Clean          bool `json:"clean"`
}

type realStressRateLimitResponse struct {
	SecondAllowedCount    int `json:"second_allowed_count"`
	SecondRejectedAtCount int `json:"second_rejected_at_count"`
	MinuteAllowedCount    int `json:"minute_allowed_count"`
	MinuteRejectedAtCount int `json:"minute_rejected_at_count"`
}

func runRealStressQuotaProbe(ctx context.Context, fixture *heldSessionSetRealFixture, agentSource string) (StressQuotaEvidence, error) {
	response, err := client.DoREST[struct{}, realStressQuotaResponse](ctx, fixture.controlPAT.Client, client.RESTRequest[struct{}]{Method: http.MethodPost, Path: "/agentcompat/io-stream-quota-probe", Body: &struct{}{}})
	if err != nil {
		return StressQuotaEvidence{}, err
	}
	rateLimit, err := client.DoREST[struct{}, realStressRateLimitResponse](ctx, fixture.controlPAT.Client, client.RESTRequest[struct{}]{Method: http.MethodPost, Path: "/agentcompat/mcp-rate-limit-probe", Body: &struct{}{}})
	if err != nil {
		return StressQuotaEvidence{}, err
	}
	pathLockProof, err := proveStressPathLockStripes(ctx, agentSource)
	if err != nil {
		return StressQuotaEvidence{}, err
	}
	return StressQuotaEvidence{PATSecond: StressQuotaBoundary{Allowed: rateLimit.SecondAllowedCount, Rejected: rateLimit.SecondRejectedAtCount, AllowedAccepted: rateLimit.SecondAllowedCount == 10, RejectedDenied: rateLimit.SecondRejectedAtCount == 11}, PATMinute: StressQuotaBoundary{Allowed: rateLimit.MinuteAllowedCount, Rejected: rateLimit.MinuteRejectedAtCount, AllowedAccepted: rateLimit.MinuteAllowedCount == 120, RejectedDenied: rateLimit.MinuteRejectedAtCount == 121}, UserStreams: StressQuotaBoundary{Allowed: response.UserAccepted, Rejected: response.UserAccepted + response.UserRejected, AllowedAccepted: response.UserAccepted == 20, RejectedDenied: response.UserRejected == 1}, ServerStreams: StressQuotaBoundary{Allowed: response.ServerAccepted, Rejected: response.ServerAccepted + response.ServerRejected, AllowedAccepted: response.ServerAccepted == 40, RejectedDenied: response.ServerRejected == 1}, PathLockStripes: pathLockProof.Stripes}, nil
}

func stressCleanupSummary(fixture *heldSessionSetRealFixture, dashboardIdentity dashboard.RuntimeIdentity, agentIdentities []agent.ProcessIdentity, workspaceRoots []string, cleanupErr error) StressCleanupSummary {
	summary := StressCleanupSummary{ReceiptCount: 1 + len(fixture.agents), WorkspaceResidue: 0}
	receipts := make([]processharness.CleanupReceipt, 0, summary.ReceiptCount)
	receipts = append(receipts, fixture.dashboard.CleanupReceipt())
	for _, instance := range fixture.agents {
		receipts = append(receipts, instance.CleanupReceipt())
	}
	for _, receipt := range receipts {
		if !receipt.Passed {
			summary.FailedReceiptCount++
		}
		if receipt.Forced {
			summary.ForcedCleanupCount++
		}
	}
	if !heldRealPIDGone(dashboardIdentity.PID) {
		summary.ProcessResidue++
	}
	if !heldRealGroupGone(dashboardIdentity.ProcessGroupID) {
		summary.ProcessGroupResidue++
	}
	for index := range fixture.agents {
		identity := agentIdentities[index]
		if !heldRealPIDGone(identity.PID) {
			summary.ProcessResidue++
		}
		if !heldRealGroupGone(identity.ProcessGroupID) {
			summary.ProcessGroupResidue++
		}
	}
	for _, root := range workspaceRoots {
		if !heldSessionSetRealWorkspaceGone(root) {
			summary.WorkspaceResidue++
		}
	}
	summary.Passed = cleanupErr == nil && summary.ReceiptCount == 9 && summary.FailedReceiptCount == 0 && summary.ForcedCleanupCount == 0 && summary.ProcessResidue == 0 && summary.ProcessGroupResidue == 0 && summary.WorkspaceResidue == 0
	return summary
}
