//go:build linux

package scenario

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestStress_PlanHasFourRoundsSixteenOpsEach(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)

	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)

	require.NoError(t, err)
	require.Len(t, plan.Rounds, 4)
	for _, round := range plan.Rounds {
		require.Len(t, round.Operations, 16)
	}
}

func TestStress_PlanUsesTwoRequestsPerPATPerRound(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)

	for _, round := range plan.Rounds {
		counts := make(map[StressPATID]int)
		kinds := make(map[StressPATID]map[StressOperationKind]int)
		for _, operation := range round.Operations {
			counts[operation.PAT]++
			if kinds[operation.PAT] == nil {
				kinds[operation.PAT] = make(map[StressOperationKind]int)
			}
			kinds[operation.PAT][operation.Kind]++
		}
		require.Len(t, counts, 8)
		for pat, count := range counts {
			require.Equal(t, 2, count)
			require.Equal(t, 1, kinds[pat][StressOperationExec])
			require.Equal(t, 1, kinds[pat][StressOperationFilesystem])
		}
	}
}

func TestStress_PlanHas64UniqueStableIDs(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)

	seen := make(map[StressOperationID]struct{})
	for _, round := range plan.Rounds {
		for _, operation := range round.Operations {
			_, duplicate := seen[operation.ID]
			require.False(t, duplicate)
			seen[operation.ID] = struct{}{}
		}
	}
	require.Len(t, seen, 64)
}

func TestStress_PlanFixedSeedReproducesByteForByte(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	first, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	second, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)

	firstJSON, err := json.Marshal(first)
	require.NoError(t, err)
	secondJSON, err := json.Marshal(second)
	require.NoError(t, err)
	require.Equal(t, firstJSON, secondJSON)
}

func TestStress_RejectsLaunchWindowOverOneSecond(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	evidence := successfulStressRoundEvidence(plan.Rounds[0])
	evidence.Operations[len(evidence.Operations)-1].LaunchedAt = evidence.Operations[0].LaunchedAt.Add(time.Second + time.Nanosecond)
	evidence.Operations[len(evidence.Operations)-1].CompletedAt = evidence.Operations[len(evidence.Operations)-1].LaunchedAt.Add(time.Millisecond)

	err = ValidateStressRoundEvidence(plan.Rounds[0], evidence)

	require.ErrorIs(t, err, ErrStressLaunchWindow)
}

func successfulStressRoundEvidence(plan StressRoundPlan) StressRoundEvidence {
	started := time.Unix(100, 0)
	operations := make([]StressOperationEvidence, len(plan.Operations))
	for index, operation := range plan.Operations {
		operations[index] = StressOperationEvidence{
			ID: operation.ID, Round: operation.Round, Agent: operation.Agent, PAT: operation.PAT, Kind: operation.Kind, SuccessProof: "ok",
			LaunchedAt: started, CompletedAt: started.Add(time.Millisecond), Succeeded: true,
		}
	}
	return StressRoundEvidence{Round: plan.Round, Operations: operations}
}
