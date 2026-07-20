//go:build linux

package scenario

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestHeldSessionSetCanonicalPlanHasExactlyFourOfEachKind(t *testing.T) {
	// Given
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)

	// When
	validated, err := validateHeldSessionSetPlans(plan)

	// Then
	require.NoError(t, err)
	require.Len(t, validated, 12)
	require.Equal(t, 4, countHeldSessionKind(validated, StressSessionTerminal))
	require.Equal(t, 4, countHeldSessionKind(validated, StressSessionNAT))
	require.Equal(t, 4, countHeldSessionKind(validated, StressSessionFM))
}

func TestHeldSessionSetRejectsInvalidTopologyBeforeConstruction(t *testing.T) {
	// Given
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)

	// When
	_, err = NewHeldSessionSet(context.Background(), HeldSessionSetInput{Plan: plan})

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidHeldSessionSetTopology)
}
