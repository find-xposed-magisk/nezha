//go:build linux && agentcompat

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

type heldSessionSetRealFixture struct {
	dashboard        *dashboard.Dashboard
	preparedBinary   *agent.PreparedBinary
	agents           []*agent.Agent
	readiness        []agent.Readiness
	agentPATs        []heldRealPATIdentity
	plan             StressPlan
	controlPAT       heldRealPATIdentity
	controlServerIDs []uint64
	closed           bool
}

func startHeldSessionSetRealFixture(ctx context.Context, paths contract.Paths, plan StressPlan) (*heldSessionSetRealFixture, error) {
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		return nil, err
	}
	fixture := &heldSessionSetRealFixture{dashboard: dashboardInstance, plan: plan}
	prepared, err := agent.PrepareBinary(ctx, paths.AgentSource().String())
	if err != nil {
		return nil, fixture.close(ctx, err)
	}
	fixture.preparedBinary = prepared
	for ordinal := 1; ordinal <= heldSessionSetAgentCount; ordinal++ {
		uuid := fmt.Sprintf("00000000-0000-0000-0000-%012d", 700+ordinal)
		instance, startErr := agent.Start(ctx, agent.AgentStartConfig{PreparedBinary: prepared, Endpoint: dashboardInstance.Endpoint(), Secret: dashboardInstance.AgentSecret(), UUID: uuid})
		if startErr != nil {
			return nil, fixture.close(ctx, startErr)
		}
		fixture.agents = append(fixture.agents, instance)
	}
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return nil, fixture.close(ctx, err)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return nil, fixture.close(ctx, err)
	}
	for index, instance := range fixture.agents {
		serverID, infoErr := dashboardInstance.WaitForInfo2UUID(ctx, instance.UUID())
		if infoErr != nil {
			return nil, fixture.close(ctx, infoErr)
		}
		pat, patErr := mintHeldRealPAT(ctx, dashboardInstance, fmt.Sprintf("held-set-agent-%d", index+1), []uint64{serverID})
		if patErr != nil {
			return nil, fixture.close(ctx, patErr)
		}
		ready, readyErr := instance.WaitReadyEventDrivenWithClient(ctx, dashboardInstance, pat.Client)
		if readyErr != nil {
			return nil, fixture.close(ctx, readyErr)
		}
		fixture.readiness = append(fixture.readiness, ready)
		fixture.agentPATs = append(fixture.agentPATs, pat)
	}
	for _, readiness := range fixture.readiness {
		fixture.controlServerIDs = append(fixture.controlServerIDs, readiness.ServerID)
	}
	fixture.controlPAT, err = mintHeldRealPAT(ctx, dashboardInstance, "held-set-control", fixture.controlServerIDs)
	if err != nil {
		return nil, fixture.close(ctx, err)
	}
	if err := validateHeldSessionSetRealFixture(fixture, plan); err != nil {
		return nil, fixture.close(ctx, err)
	}
	return fixture, nil
}

func validateHeldSessionSetRealFixture(fixture *heldSessionSetRealFixture, plan StressPlan) error {
	if fixture == nil || fixture.dashboard == nil || fixture.preparedBinary == nil || len(fixture.agents) != heldSessionSetAgentCount || len(fixture.readiness) != heldSessionSetAgentCount || len(fixture.agentPATs) != heldSessionSetAgentCount || len(fixture.controlServerIDs) != heldSessionSetAgentCount {
		return errors.New("held session set real fixture is incomplete")
	}
	if plan.Profile != contract.ProfilePRFull || len(plan.Sessions) != 12 {
		return errors.New("held session set real fixture received noncanonical plan")
	}
	seenServers := make(map[uint64]struct{}, len(fixture.controlServerIDs))
	for index, readiness := range fixture.readiness {
		if readiness.ServerID == 0 || readiness.UUID != fixture.agents[index].UUID() || !fixture.agentPATs[index].IdentitySeen || !slices.Equal(fixture.agentPATs[index].ServerIDs, []uint64{readiness.ServerID}) {
			return errors.New("held session set real fixture PAT mapping is invalid")
		}
		if _, exists := seenServers[readiness.ServerID]; exists {
			return errors.New("held session set real fixture server IDs are not unique")
		}
		seenServers[readiness.ServerID] = struct{}{}
	}
	if !fixture.controlPAT.IdentitySeen || !slices.Equal(fixture.controlPAT.ServerIDs, fixture.controlServerIDs) {
		return errors.New("held session set real fixture control PAT mapping is invalid")
	}
	return nil
}

func (fixture *heldSessionSetRealFixture) input(plan StressPlan) (HeldSessionSetInput, error) {
	topology := make([]HeldSessionAgent, len(fixture.agents))
	for index, instance := range fixture.agents {
		ordinal, err := NewStressAgentOrdinal(index + 1)
		if err != nil {
			return HeldSessionSetInput{}, err
		}
		topology[index] = HeldSessionAgent{Ordinal: ordinal, Agent: instance, Readiness: fixture.readiness[index], PATClient: fixture.agentPATs[index].Client}
	}
	return HeldSessionSetInput{Dashboard: fixture.dashboard, Plan: plan, Topology: topology, ControlClient: fixture.controlPAT.Client, ControlServerIDs: append([]uint64(nil), fixture.controlServerIDs...), Dependencies: defaultHeldSessionSetDependencies()}, nil
}

func (fixture *heldSessionSetRealFixture) close(ctx context.Context, cause error) error {
	if fixture == nil || fixture.closed {
		return cause
	}
	fixture.closed = true
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	joined := cause
	for index := len(fixture.agents) - 1; index >= 0; index-- {
		joined = errors.Join(joined, fixture.agents[index].Stop(cleanupContext))
	}
	if fixture.preparedBinary != nil {
		joined = errors.Join(joined, fixture.preparedBinary.Close())
	}
	if fixture.dashboard != nil {
		joined = errors.Join(joined, fixture.dashboard.Stop(cleanupContext))
	}
	return joined
}

func heldSessionSetRealWorkspaceGone(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}
