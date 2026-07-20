//go:build linux

package scenario

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestStressEvidence_AcceptsCompleteSuccessContract(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	evidence := validStressEvidence(t, profile)

	err = evidence.ValidateSuccess(profile)

	require.NoError(t, err)
}

func TestStressEvidence_AcceptsOnlyExactStressWorkerFault(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	evidence := validStressEvidence(t, profile)
	target := StressWorkerFaultTarget()
	evidence.FaultTarget = &target
	faultOperation := findStressFaultOperation(evidence.Plan)
	for roundIndex := range evidence.Iterations[0].Rounds {
		for operationIndex := range evidence.Iterations[0].Rounds[roundIndex].Operations {
			operation := &evidence.Iterations[0].Rounds[roundIndex].Operations[operationIndex]
			if operation.ID == faultOperation.ID {
				operation.Succeeded = false
				operation.Error = "injected stress worker fault"
			}
		}
	}

	err = evidence.ValidateStressWorker(profile)

	require.NoError(t, err)
}

func TestStressEvidence_RejectsSecondFailedOperationForStressWorker(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	evidence := validStressEvidence(t, profile)
	target := StressWorkerFaultTarget()
	evidence.FaultTarget = &target
	evidence.Iterations[0].Rounds[1].Operations[0].Succeeded = false
	evidence.Iterations[0].Rounds[1].Operations[0].Error = "injected stress worker fault"
	evidence.Iterations[0].Rounds[1].Operations[1].Succeeded = false
	evidence.Iterations[0].Rounds[1].Operations[1].Error = "unexpected failure"

	err = evidence.ValidateStressWorker(profile)

	require.ErrorIs(t, err, ErrStressFault)
}

func TestStressEvidence_RejectsSelfConsistentTruncatedPlan(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	evidence := validStressEvidence(t, profile)
	evidence.Plan.Rounds = evidence.Plan.Rounds[:1]
	evidence.Iterations[0].Rounds = evidence.Iterations[0].Rounds[:1]

	err = evidence.ValidateSuccess(profile)

	require.ErrorIs(t, err, ErrStressEvidence)
}

func TestStressEvidence_RejectsDuplicateDashboardResources(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	evidence := validStressEvidence(t, profile)
	evidence.Iterations[0].Resources[1] = evidence.Iterations[0].Resources[0]

	err = evidence.ValidateSuccess(profile)

	require.ErrorIs(t, err, ErrStressEvidence)
}

func validStressEvidence(t *testing.T, profile contract.Profile) StressEvidence {
	t.Helper()
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	warmups := make([]StressWarmupEvidence, profile.AgentCount())
	for index := range warmups {
		agent, agentErr := NewStressAgentOrdinal(index + 1)
		require.NoError(t, agentErr)
		warmups[index] = StressWarmupEvidence{Agent: agent, Exec: true, Filesystem: true, Terminal: true, NAT: true, FM: true}
	}
	sessions := make([]StressSessionEvidence, len(plan.Sessions))
	for index, session := range plan.Sessions {
		sessions[index] = StressSessionEvidence{ID: session.ID, Kind: session.Kind, Succeeded: true}
	}
	rounds := make([]StressRoundEvidence, len(plan.Rounds))
	started := time.Unix(100, 0)
	for roundIndex, round := range plan.Rounds {
		operations := make([]StressOperationEvidence, len(round.Operations))
		for operationIndex, operation := range round.Operations {
			operations[operationIndex] = StressOperationEvidence{ID: operation.ID, Round: operation.Round, Agent: operation.Agent, PAT: operation.PAT, Kind: operation.Kind, LaunchedAt: started, CompletedAt: started.Add(time.Millisecond), Succeeded: true, SuccessProof: "ok"}
		}
		rounds[roundIndex] = StressRoundEvidence{Round: round.Round, Operations: operations}
	}
	resources := make([]StressProcessWindows, 0, profile.AgentCount()+1)
	resources = append(resources, stressDashboardResourceFixture(100))
	for index := 1; index <= profile.AgentCount(); index++ {
		agent, agentErr := NewStressAgentOrdinal(index)
		require.NoError(t, agentErr)
		process, processErr := NewStressAgentProcess(agent, 200+index)
		require.NoError(t, processErr)
		resources = append(resources, StressProcessWindows{Process: process, Baseline: stressWindow(200+index, 100), End: stressWindow(200+index, 100)})
	}
	iterations := make([]StressIterationEvidence, profile.Iterations())
	for index := range iterations {
		iterations[index] = StressIterationEvidence{Iteration: index + 1, Rounds: rounds, Resources: resources}
	}
	return StressEvidence{
		Version: 1, Profile: profile.Name(), Seed: contract.DefaultSeed, Plan: plan,
		PreparedBinaries: StressPreparedBinaries{DashboardBuildCount: 1, DashboardPathReused: true, AgentBuildCount: 1, AgentPathReused: true},
		Quotas: StressQuotaEvidence{
			PATSecond: quotaBoundary(10, 11), PATMinute: quotaBoundary(120, 121),
			UserStreams: quotaBoundary(20, 21), ServerStreams: quotaBoundary(40, 41), PathLockStripes: 1024,
		},
		Warmups: warmups, Sessions: sessions, Iterations: iterations,
		Cleanup: StressCleanupSummary{Passed: true, ReceiptCount: 9},
	}
}

func quotaBoundary(allowed, rejected int) StressQuotaBoundary {
	return StressQuotaBoundary{Allowed: allowed, Rejected: rejected, AllowedAccepted: true, RejectedDenied: true}
}

func findStressFaultOperation(plan StressPlan) StressOperationPlan {
	target := StressWorkerFaultTarget()
	for _, round := range plan.Rounds {
		for _, operation := range round.Operations {
			if round.Round == target.Round && operation.Agent == target.Agent && operation.Kind == target.Kind {
				return operation
			}
		}
	}
	panic("stress fault operation missing")
}
