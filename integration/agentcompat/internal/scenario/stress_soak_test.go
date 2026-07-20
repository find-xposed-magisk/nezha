//go:build linux

package scenario

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestStress_RejectsStrictThreePointDashboardRSSIncrease(t *testing.T) {
	identity, err := NewStressDashboardProcess(101)
	require.NoError(t, err)
	trend := StressSoakTrendEvidence{Series: []StressRSSSeries{{Process: identity, EndRSSBytes: [3]uint64{100, 101, 102}}}}

	err = ValidateStressSoakTrend(trend)

	require.ErrorIs(t, err, ErrStressSoakTrend)
}

func TestStress_RejectsStrictThreePointAgentRSSIncrease(t *testing.T) {
	agent, err := NewStressAgentOrdinal(4)
	require.NoError(t, err)
	identity, err := NewStressAgentProcess(agent, 404)
	require.NoError(t, err)
	trend := StressSoakTrendEvidence{Series: []StressRSSSeries{{Process: identity, EndRSSBytes: [3]uint64{200, 300, 400}}}}

	err = ValidateStressSoakTrend(trend)

	require.ErrorIs(t, err, ErrStressSoakTrend)
}

func TestStress_AcceptsStableAndNonMonotonicSoakTrend(t *testing.T) {
	dashboard, err := NewStressDashboardProcess(101)
	require.NoError(t, err)
	agentOrdinal, err := NewStressAgentOrdinal(1)
	require.NoError(t, err)
	agent, err := NewStressAgentProcess(agentOrdinal, 201)
	require.NoError(t, err)
	trend := StressSoakTrendEvidence{Series: []StressRSSSeries{
		{Process: dashboard, EndRSSBytes: [3]uint64{100, 100, 100}},
		{Process: agent, EndRSSBytes: [3]uint64{200, 220, 210}},
	}}

	err = ValidateStressSoakTrend(trend)

	require.NoError(t, err)
}

func TestStressSoak_RejectsMissingUnknownAndDuplicateSeries(t *testing.T) {
	profile := mustProfile(t, contract.ProfileSoak)
	dashboard, err := NewStressDashboardProcess(101)
	require.NoError(t, err)
	series := StressRSSSeries{Process: dashboard, EndRSSBytes: [3]uint64{100, 100, 100}}

	for _, mutate := range []func([]StressRSSSeries) []StressRSSSeries{
		func(values []StressRSSSeries) []StressRSSSeries { return values[:1] },
		func(values []StressRSSSeries) []StressRSSSeries {
			agent, _ := NewStressAgentOrdinal(999)
			process, _ := NewStressAgentProcess(agent, 999)
			return append(values, StressRSSSeries{Process: process})
		},
		func(values []StressRSSSeries) []StressRSSSeries { return append(values, values[0]) },
	} {
		values := make([]StressRSSSeries, 0, profile.AgentCount()+1)
		values = append(values, series)
		for index := 1; index <= profile.AgentCount(); index++ {
			agent, agentErr := NewStressAgentOrdinal(index)
			require.NoError(t, agentErr)
			process, processErr := NewStressAgentProcess(agent, 200+index)
			require.NoError(t, processErr)
			values = append(values, StressRSSSeries{Process: process, EndRSSBytes: [3]uint64{100, 100, 100}})
		}
		err = ValidateStressSoakTrendForProfile(profile, StressSoakTrendEvidence{Series: mutate(values)})
		require.Error(t, err)
	}
}

func mustProfile(t *testing.T, name contract.ProfileName) contract.Profile {
	t.Helper()
	profile, err := contract.ProfileByName(string(name))
	require.NoError(t, err)
	return profile
}
