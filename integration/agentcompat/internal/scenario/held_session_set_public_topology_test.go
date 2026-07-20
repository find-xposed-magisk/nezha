//go:build linux

package scenario

import (
	"context"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/stretchr/testify/require"
)

func TestHeldSessionSetPublicTopologyRejectsAuthorityMutationsWithoutSideEffects(t *testing.T) {
	cases := []struct {
		name            string
		mutate          func(*heldSessionSetPublicFixture)
		expectedInspect int
		expectedError   error
	}{
		{name: "wrong profile", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.Plan.Profile = "missing-profile" }, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetPlan},
		{name: "missing ordinal", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Topology = fixture.input.Topology[:7]
		}, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "duplicate ordinal", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Topology[1].Ordinal = fixture.input.Topology[0].Ordinal
		}, expectedInspect: 1, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "duplicate UUID", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Topology[1].Readiness.UUID = fixture.input.Topology[0].Readiness.UUID
		}, expectedInspect: 2, expectedError: ErrHeldReadinessAgentMismatch},
		{name: "duplicate server ID", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Topology[1].Readiness.ServerID = fixture.input.Topology[0].Readiness.ServerID
		}, expectedInspect: 2, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "extra control server", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.ControlServerIDs = append(fixture.input.ControlServerIDs, 99)
		}, expectedInspect: 8, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "duplicate control server", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.ControlServerIDs[1] = fixture.input.ControlServerIDs[0]
		}, expectedInspect: 8, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "noncanonical plan", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.Plan.Sessions[0].Ordinal++ }, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetPlan},
		{name: "duplicate plan", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Plan.Sessions[1].ID = fixture.input.Plan.Sessions[0].ID
		}, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetPlan},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newHeldSessionSetPublicFixture(t)
			testCase.mutate(fixture)
			_, err := NewHeldSessionSet(context.Background(), fixture.input)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrHeldSessionSetOperation)
			require.ErrorIs(t, err, testCase.expectedError)
			require.Empty(t, fixture.state.present)
			require.Empty(t, fixture.state.absent)
			require.Empty(t, fixture.coordinator.ready)
			counters := fixture.counters.values()
			require.Zero(t, counters.snapshot)
			require.Zero(t, counters.observe)
			require.Equal(t, testCase.expectedInspect, counters.inspect)
			require.Zero(t, counters.terminal)
			require.Zero(t, counters.nat)
			require.Zero(t, counters.fm)
		})
	}
}

func TestHeldSessionSetPublicTopologyRejectsBoundaryInputsWithoutConstruction(t *testing.T) {
	cases := []struct {
		name            string
		mutate          func(*heldSessionSetPublicFixture)
		expectedInspect int
		expectedError   error
	}{
		{name: "nil dashboard", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.Dashboard = nil }, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "nil control client", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.ControlClient = nil }, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "nil agent", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.Topology[0].Agent = nil }, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "nil pat", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.Topology[0].PATClient = nil }, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "invalid pid", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Dependencies.InspectAgent = func(*agent.Agent) heldSessionAgentFacts { return heldSessionAgentFacts{PID: 0, UUID: "uuid-a"} }
		}, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "readiness mismatch", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.Topology[0].Readiness.UUID = "different" }, expectedInspect: 1, expectedError: ErrInvalidHeldReadiness},
		{name: "incomplete readiness", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Topology[0].Readiness.VersionObserved = false
		}, expectedInspect: 1, expectedError: ErrInvalidHeldReadiness},
		{name: "zero control server", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.ControlServerIDs[0] = 0 }, expectedInspect: 8, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "unknown control server", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.ControlServerIDs[0] = 991 }, expectedInspect: 8, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "missing control server", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.ControlServerIDs = fixture.input.ControlServerIDs[:7]
		}, expectedInspect: 8, expectedError: ErrInvalidHeldSessionSetTopology},
		{name: "plan kind", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Plan.Sessions[0].Kind = StressSessionKind("unknown")
		}, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetPlan},
		{name: "plan agent", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Plan.Sessions[0].Agent = fixture.input.Plan.Sessions[1].Agent
		}, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetPlan},
		{name: "plan ordinal", mutate: func(fixture *heldSessionSetPublicFixture) { fixture.input.Plan.Sessions[0].Ordinal++ }, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetPlan},
		{name: "plan duplicate id", mutate: func(fixture *heldSessionSetPublicFixture) {
			fixture.input.Plan.Sessions[1].ID = fixture.input.Plan.Sessions[0].ID
		}, expectedInspect: 0, expectedError: ErrInvalidHeldSessionSetPlan},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newHeldSessionSetPublicFixture(t)
			testCase.mutate(fixture)
			_, err := NewHeldSessionSet(context.Background(), fixture.input)
			require.Error(t, err)
			require.ErrorIs(t, err, testCase.expectedError)
			counters := fixture.counters.values()
			require.Equal(t, testCase.expectedInspect, counters.inspect)
			require.Zero(t, counters.snapshot)
			require.Zero(t, counters.observe)
			require.Zero(t, counters.terminal)
			require.Zero(t, counters.nat)
			require.Zero(t, counters.fm)
		})
	}
}
