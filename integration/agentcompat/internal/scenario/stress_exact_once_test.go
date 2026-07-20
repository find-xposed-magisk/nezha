//go:build linux

package scenario

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestStressExactOnceRegistryRejectsInvalidReceipts(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	registry, err := newStressExactOnceRegistry(plan)
	require.NoError(t, err)
	operation := plan.Rounds[0].Operations[0]
	receipt := StressOperationReceipt{Operation: operation, SuccessProof: "ok"}
	require.NoError(t, registry.Start(operation))
	require.ErrorIs(t, registry.Start(operation), ErrStressOperationDuplicateStart)
	require.NoError(t, registry.Complete(receipt))
	require.ErrorIs(t, registry.Complete(receipt), ErrStressOperationDuplicateCompletion)
	require.ErrorIs(t, registry.Start(StressOperationPlan{ID: operation.ID}), ErrStressOperationOwnerMismatch)
	require.ErrorIs(t, registry.Complete(StressOperationReceipt{Operation: StressOperationPlan{ID: operation.ID, Round: 2, Agent: operation.Agent, PAT: operation.PAT, Kind: operation.Kind}, SuccessProof: "ok"}), ErrStressOperationOwnerMismatch)
}

func TestStressExactOnceRegistryRequiresCanonicalCompletion(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	registry, err := newStressExactOnceRegistry(plan)
	require.NoError(t, err)
	for _, round := range plan.Rounds {
		for _, operation := range round.Operations {
			require.NoError(t, registry.Start(operation))
		}
	}
	require.ErrorIs(t, registry.ValidateComplete(), ErrStressOperationMissingCompletion)
}
