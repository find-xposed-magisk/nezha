//go:build linux

package scenario

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

type heldSessionSetPublicFixture struct {
	plan        StressPlan
	input       HeldSessionSetInput
	coordinator *heldSessionSetConstructorCoordinator
	state       *heldSessionSetStateFake
	closeOrder  chan int
	waitOrder   chan int
	counters    *heldSessionSetCallCounters
}

type heldSessionSetCallCounters struct {
	mu        sync.Mutex
	inspect   int
	snapshot  int
	observe   int
	waitState int
	terminal  int
	nat       int
	fm        int
}

func (counters *heldSessionSetCallCounters) add(field *int) {
	counters.mu.Lock()
	(*field)++
	counters.mu.Unlock()
}

func (counters *heldSessionSetCallCounters) values() heldSessionSetCallCounters {
	counters.mu.Lock()
	defer counters.mu.Unlock()
	return heldSessionSetCallCounters{inspect: counters.inspect, snapshot: counters.snapshot, observe: counters.observe, waitState: counters.waitState, terminal: counters.terminal, nat: counters.nat, fm: counters.fm}
}

func (fixture *heldSessionSetPublicFixture) returnedSet(t *testing.T) *heldSessionSet {
	t.Helper()
	result := make(chan *heldSessionSet, 1)
	errResult := make(chan error, 1)
	go func() {
		set, err := NewHeldSessionSet(context.Background(), fixture.input)
		result <- set
		errResult <- err
	}()
	for range fixture.plan.Sessions {
		<-fixture.coordinator.ready
	}
	fixture.releaseConstructors()
	set := <-result
	if err := <-errResult; err != nil {
		t.Fatal(err)
	}
	return set
}

func newHeldSessionSetPublicFixture(t *testing.T) *heldSessionSetPublicFixture {
	t.Helper()
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := newHeldSessionSetConstructorCoordinator()
	closeOrder := make(chan int, len(plan.Sessions))
	waitOrder := make(chan int, len(plan.Sessions))
	for index, sessionPlan := range plan.Sessions {
		coordinator.indices[sessionPlan.ID.String()] = index
		coordinator.sessions[index] = newHeldSessionSetTestSession(index, sessionPlan, closeOrder, waitOrder)
	}
	topology := make([]HeldSessionAgent, heldSessionSetAgentCount)
	agentFacts := make(map[*agent.Agent]heldSessionAgentFacts, heldSessionSetAgentCount)
	controlServerIDs := make([]uint64, heldSessionSetAgentCount)
	for index := range topology {
		ordinal, ordinalErr := NewStressAgentOrdinal(index + 1)
		if ordinalErr != nil {
			t.Fatal(ordinalErr)
		}
		agentInstance := &agent.Agent{}
		agentUUID := "uuid-" + string(rune('a'+index))
		topology[index] = HeldSessionAgent{Ordinal: ordinal, Agent: agentInstance, PATClient: &client.Client{}, Readiness: agent.Readiness{ServerID: uint64(index + 1), UUID: agentUUID, Version: "test", Online: true, VersionObserved: true, RequestTaskEstablished: true, StateReceiptObserved: true}}
		agentFacts[agentInstance] = heldSessionAgentFacts{PID: 1, UUID: agentUUID}
		controlServerIDs[index] = uint64(index + 1)
	}
	state := &heldSessionSetStateFake{baseline: client.IOStreamState{Count: 7}, expectedPresentCount: 19, expectedAbsentCount: 7, presentStreamErrors: make(map[string]error), absentStreamErrors: make(map[string]error), aggregatePredicatesEmpty: true, waitCalls: make(chan client.IOStreamStateExpectation, 40)}
	counters := &heldSessionSetCallCounters{}
	fixture := &heldSessionSetPublicFixture{plan: plan, coordinator: coordinator, state: state, closeOrder: closeOrder, waitOrder: waitOrder, counters: counters}
	dependencies := fixture.dependencies()
	dependencies.InspectAgent = func(instance *agent.Agent) heldSessionAgentFacts {
		counters.add(&counters.inspect)
		return agentFacts[instance]
	}
	dependencies.ObserveState = func(*client.Client) heldSessionSetStateObserver { counters.add(&counters.observe); return state }
	fixture.input = HeldSessionSetInput{Dashboard: &dashboard.Dashboard{}, Plan: plan, Topology: topology, ControlClient: &client.Client{}, ControlServerIDs: controlServerIDs, Dependencies: dependencies}
	return fixture
}

func (fixture *heldSessionSetPublicFixture) dependencies() HeldSessionSetDependencies {
	construct := func(ctx, lifetimeContext context.Context, plan StressSessionPlan) (heldSession, error) {
		fixture.coordinator.contextSeen <- lifetimeContext
		return fixture.coordinator.construct(ctx, plan)
	}
	return HeldSessionSetDependencies{
		Terminal: func(ctx context.Context, input heldTerminalInput) (heldSession, error) {
			fixture.counters.add(&fixture.counters.terminal)
			return construct(ctx, input.LifetimeContext, input.Plan)
		},
		NAT: func(ctx context.Context, input heldNATInput) (heldSession, error) {
			fixture.counters.add(&fixture.counters.nat)
			return construct(ctx, input.LifetimeContext, input.Plan)
		},
		FM: func(ctx context.Context, input heldLegacyFMInput) (heldSession, error) {
			fixture.counters.add(&fixture.counters.fm)
			return construct(ctx, input.LifetimeContext, input.Plan)
		},
		Snapshot: func(context.Context, heldSessionSetStateObserver) (client.IOStreamState, error) {
			fixture.counters.add(&fixture.counters.snapshot)
			return fixture.state.baseline, fixture.state.snapshotError
		},
		WaitState: func(ctx context.Context, state heldSessionSetStateObserver, expectation client.IOStreamStateExpectation) (client.IOStreamState, error) {
			fixture.counters.add(&fixture.counters.waitState)
			return state.WaitForIOStreamState(ctx, expectation)
		},
	}
}

func (fixture *heldSessionSetPublicFixture) releaseConstructors() {
	fixture.coordinator.releaseAll()
	for index := range fixture.plan.Sessions {
		fixture.coordinator.release(index)
	}
}

func sensitiveError(label string) error {
	return errors.New(label + " token=secret-token path=/secret/path uuid=secret-uuid stream=secret-stream")
}
