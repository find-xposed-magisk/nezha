//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

var (
	ErrInvalidHeldSessionSetTopology = errors.New("held session set topology is invalid")
	ErrInvalidHeldSessionSetPlan     = errors.New("held session set plan is invalid")
)

const heldSessionSetAgentCount = 8

type HeldSessionAgent struct {
	Ordinal   StressAgentOrdinal
	Agent     *agent.Agent
	Readiness agent.Readiness
	PATClient *client.Client
}

type HeldSessionSetInput struct {
	Dashboard                          *dashboard.Dashboard
	Plan                               StressPlan
	Topology                           []HeldSessionAgent
	Dependencies                       HeldSessionSetDependencies
	ControlClient                      *client.Client
	ControlServerIDs                   []uint64
	testHealthSnapshotRequestHook      func(int)
	testHealthSnapshotReplyHook        func(int)
	testHealthSnapshotOverrideHook     func(int, heldHealthSnapshotRequest) *heldHealthSnapshot
	testHealthSnapshotSendHook         func(int)
	testHealthEventHook                func(heldHealthMessage)
	testHealthClosureObservedHook      func(int)
	testHealthSnapshotAcceptedHook     func(heldHealthSnapshot)
	testHealthShutdownAcceptedHook     func()
	testHealthShutdownAcknowledgedHook func()
}

type heldSessionSetTopology struct {
	dashboard   *dashboard.Dashboard
	stateClient heldSessionSetStateObserver
	agents      map[int]HeldSessionAgent
}

type heldSessionSetStateObserver interface {
	IOStreamState(context.Context) (client.IOStreamState, error)
	WaitForIOStreamState(context.Context, client.IOStreamStateExpectation) (client.IOStreamState, error)
}

type heldSessionSetAuthorizedObserver struct {
	client *client.Client
}

func (observer heldSessionSetAuthorizedObserver) IOStreamState(ctx context.Context) (client.IOStreamState, error) {
	return observer.client.IOStreamState(ctx)
}

func (observer heldSessionSetAuthorizedObserver) WaitForIOStreamState(ctx context.Context, expectation client.IOStreamStateExpectation) (client.IOStreamState, error) {
	return observer.client.WaitForIOStreamState(ctx, expectation)
}

type HeldSessionSetDependencies struct {
	Terminal     func(context.Context, heldTerminalInput) (heldSession, error)
	NAT          func(context.Context, heldNATInput) (heldSession, error)
	FM           func(context.Context, heldLegacyFMInput) (heldSession, error)
	Snapshot     func(context.Context, heldSessionSetStateObserver) (client.IOStreamState, error)
	WaitState    func(context.Context, heldSessionSetStateObserver, client.IOStreamStateExpectation) (client.IOStreamState, error)
	InspectAgent func(*agent.Agent) heldSessionAgentFacts
	ObserveState func(*client.Client) heldSessionSetStateObserver
}

type heldSessionAgentFacts struct {
	PID  int
	UUID string
}

func defaultHeldSessionSetDependencies() HeldSessionSetDependencies {
	return HeldSessionSetDependencies{
		Terminal: func(ctx context.Context, input heldTerminalInput) (heldSession, error) {
			return newHeldTerminalSession(ctx, input)
		},
		NAT: func(ctx context.Context, input heldNATInput) (heldSession, error) {
			return newHeldNATSession(ctx, input)
		},
		FM: func(ctx context.Context, input heldLegacyFMInput) (heldSession, error) {
			return newHeldLegacyFMSession(ctx, input)
		},
		Snapshot: func(ctx context.Context, stateClient heldSessionSetStateObserver) (client.IOStreamState, error) {
			return stateClient.IOStreamState(ctx)
		},
		WaitState: func(ctx context.Context, stateClient heldSessionSetStateObserver, expectation client.IOStreamStateExpectation) (client.IOStreamState, error) {
			return stateClient.WaitForIOStreamState(ctx, expectation)
		},
		InspectAgent: func(instance *agent.Agent) heldSessionAgentFacts {
			return heldSessionAgentFacts{PID: instance.PID(), UUID: instance.UUID()}
		},
		ObserveState: func(controlClient *client.Client) heldSessionSetStateObserver {
			return heldSessionSetAuthorizedObserver{client: controlClient}
		},
	}
}

func validateHeldSessionSetPlans(plan StressPlan) ([]StressSessionPlan, error) {
	canonical, err := canonicalHeldSessionPlans(plan)
	if err != nil {
		return nil, errors.Join(ErrInvalidHeldSessionSetPlan, err)
	}
	if !reflect.DeepEqual(canonical, plan.Sessions) || len(plan.Sessions) != 12 {
		return nil, ErrInvalidHeldSessionSetPlan
	}
	ids := make(map[string]struct{}, len(plan.Sessions))
	for _, session := range plan.Sessions {
		if session.ID.String() == "" {
			return nil, fmt.Errorf("empty session plan ID: %w", ErrInvalidHeldSessionSetPlan)
		}
		if _, exists := ids[session.ID.String()]; exists {
			return nil, fmt.Errorf("duplicate session plan ID: %s: %w", session.ID.String(), ErrInvalidHeldSessionSetPlan)
		}
		ids[session.ID.String()] = struct{}{}
	}
	if countHeldSessionKind(plan.Sessions, StressSessionTerminal) != 4 || countHeldSessionKind(plan.Sessions, StressSessionNAT) != 4 || countHeldSessionKind(plan.Sessions, StressSessionFM) != 4 {
		return nil, ErrInvalidHeldSessionSetPlan
	}
	return append([]StressSessionPlan(nil), plan.Sessions...), nil
}

func canonicalHeldSessionPlans(plan StressPlan) ([]StressSessionPlan, error) {
	if plan.Seed == 0 || plan.Profile == "" {
		return nil, ErrInvalidHeldSessionSetPlan
	}
	profile, err := contract.ProfileByName(string(plan.Profile))
	if err != nil {
		return nil, err
	}
	canonical, err := GenerateStressPlan(profile, plan.Seed)
	if err != nil {
		return nil, err
	}
	return canonical.Sessions, nil
}

func countHeldSessionKind(plans []StressSessionPlan, kind StressSessionKind) int {
	count := 0
	for _, plan := range plans {
		if plan.Kind == kind {
			count++
		}
	}
	return count
}

func validateHeldSessionSetTopology(input HeldSessionSetInput, plans []StressSessionPlan) (heldSessionSetTopology, error) {
	if input.Dashboard == nil || input.ControlClient == nil {
		return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
	}
	inspectAgent := input.Dependencies.InspectAgent
	if inspectAgent == nil {
		inspectAgent = defaultHeldSessionSetDependencies().InspectAgent
	}
	observeState := input.Dependencies.ObserveState
	if observeState == nil {
		observeState = defaultHeldSessionSetDependencies().ObserveState
	}
	profile, err := contract.ProfileByName(string(input.Plan.Profile))
	if err != nil || profile.AgentCount() != heldSessionSetAgentCount || len(input.Topology) != heldSessionSetAgentCount {
		return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
	}
	agents := make(map[int]HeldSessionAgent, len(input.Topology))
	uuids := make(map[string]struct{}, len(input.Topology))
	serverIDs := make(map[uint64]struct{}, len(input.Topology))
	for _, topology := range input.Topology {
		ordinal := topology.Ordinal.Int()
		if ordinal < 1 || ordinal > heldSessionSetAgentCount || topology.Agent == nil || topology.PATClient == nil {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
		if _, exists := agents[ordinal]; exists {
			return heldSessionSetTopology{}, fmt.Errorf("duplicate agent ordinal %d: %w", ordinal, ErrInvalidHeldSessionSetTopology)
		}
		agentFacts := inspectAgent(topology.Agent)
		if err := validateHeldReadinessFacts(agentFacts, topology.Readiness); err != nil {
			return heldSessionSetTopology{}, err
		}
		if agentFacts.PID < 1 || topology.Readiness.ServerID == 0 || topology.Readiness.UUID != agentFacts.UUID {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
		if _, exists := uuids[topology.Readiness.UUID]; exists {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
		if _, exists := serverIDs[topology.Readiness.ServerID]; exists {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
		uuids[topology.Readiness.UUID] = struct{}{}
		serverIDs[topology.Readiness.ServerID] = struct{}{}
		agents[ordinal] = topology
	}
	if len(input.ControlServerIDs) != heldSessionSetAgentCount {
		return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
	}
	controlServerIDs := make(map[uint64]struct{}, len(input.ControlServerIDs))
	for _, serverID := range input.ControlServerIDs {
		if serverID == 0 {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
		if _, exists := serverIDs[serverID]; !exists {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
		if _, exists := controlServerIDs[serverID]; exists {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
		controlServerIDs[serverID] = struct{}{}
	}
	for ordinal := 1; ordinal <= heldSessionSetAgentCount; ordinal++ {
		if _, exists := agents[ordinal]; !exists {
			return heldSessionSetTopology{}, ErrInvalidHeldSessionSetTopology
		}
	}
	for _, plan := range plans {
		if _, exists := agents[plan.Agent.Int()]; !exists {
			return heldSessionSetTopology{}, fmt.Errorf("missing agent ordinal %d: %w", plan.Agent.Int(), ErrInvalidHeldSessionSetTopology)
		}
	}
	return heldSessionSetTopology{dashboard: input.Dashboard, stateClient: observeState(input.ControlClient), agents: agents}, nil
}
