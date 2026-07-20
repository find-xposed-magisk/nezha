//go:build linux && agentcompat

package scenario

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestHeldSessionSetEightAgentFourFourFour(t *testing.T) {
	requireHeldRealSources(t)
	paths, err := contract.NewPaths(os.Getenv("AGENTCOMPAT_NEZHA_SOURCE"), os.Getenv("AGENTCOMPAT_AGENT_SOURCE"), t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	plan, err := GenerateStressPlan(profile, contract.DefaultSeed)
	require.NoError(t, err)
	realFixture, err := startHeldSessionSetRealFixture(ctx, paths, plan)
	require.NoError(t, err)
	dashboardIdentity := realFixture.dashboard.RuntimeIdentity()
	agentIdentities := make([]agent.ProcessIdentity, len(realFixture.agents))
	workspaceRoots := make([]string, 0, len(realFixture.agents)+2)
	workspaceRoots = append(workspaceRoots, realFixture.dashboard.WorkspaceRoot(), realFixture.preparedBinary.WorkspaceRoot())
	for index, instance := range realFixture.agents {
		agentIdentities[index] = instance.RuntimeIdentity()
		workspaceRoots = append(workspaceRoots, instance.WorkspaceRoot())
	}
	t.Cleanup(func() { _ = realFixture.close(context.Background(), nil) })
	input, err := realFixture.input(plan)
	require.NoError(t, err)
	baseline, err := realFixture.controlPAT.Client.IOStreamState(ctx)
	require.NoError(t, err)
	set, err := NewHeldSessionSet(ctx, input)
	require.NoError(t, err)
	require.Len(t, set.sessions, 12)
	for index, session := range set.sessions {
		require.Equal(t, plan.Sessions[index], session.Plan())
	}
	select {
	case <-set.Done():
		t.Fatal("held session set health completed while sessions were live")
	default:
	}
	streamIDs := make([]string, len(set.sessions))
	seen := make(map[string]struct{}, len(set.sessions))
	protocolProved := true
	for index, session := range set.sessions {
		streamID, present := session.IOStreamID()
		require.True(t, present)
		require.NotEmpty(t, streamID)
		require.NotContains(t, seen, streamID)
		seen[streamID] = struct{}{}
		streamIDs[index] = streamID
		protocolProved = protocolProved && heldRealSessionProtocolProved(session)
	}
	require.True(t, protocolProved)
	live, err := realFixture.controlPAT.Client.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count + 12)})
	require.NoError(t, err)
	require.Equal(t, baseline.Count+12, live.Count)
	for _, streamID := range streamIDs {
		_, err := realFixture.controlPAT.Client.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count + 12), PresentStreamID: streamID})
		require.NoError(t, err)
	}
	require.Equal(t, dashboardIdentity, realFixture.dashboard.RuntimeIdentity())
	for index, instance := range realFixture.agents {
		require.Equal(t, agentIdentities[index], instance.RuntimeIdentity())
	}
	require.Equal(t, 4, countHeldSessionKind(plan.Sessions, StressSessionTerminal))
	require.Equal(t, 4, countHeldSessionKind(plan.Sessions, StressSessionNAT))
	require.Equal(t, 4, countHeldSessionKind(plan.Sessions, StressSessionFM))

	require.NoError(t, set.Close(ctx))
	require.NoError(t, set.WaitHealthy(ctx))
	closed, err := realFixture.controlPAT.Client.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count)})
	require.NoError(t, err)
	require.Equal(t, baseline.Count, closed.Count)
	for _, streamID := range streamIDs {
		_, err := realFixture.controlPAT.Client.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count), AbsentStreamID: streamID})
		require.NoError(t, err)
	}
	require.Equal(t, dashboardIdentity, realFixture.dashboard.RuntimeIdentity())
	for index, instance := range realFixture.agents {
		require.Equal(t, agentIdentities[index], instance.RuntimeIdentity())
	}
	resourcesAbsent := heldRealSessionResourcesAbsent(ctx, realFixture, set.sessions)
	require.True(t, resourcesAbsent)

	cleanupErr := realFixture.close(ctx, nil)
	require.NoError(t, cleanupErr)
	cleanupOK := realFixture.dashboard.CleanupReceipt().Passed && !realFixture.dashboard.CleanupReceipt().Forced
	for _, instance := range realFixture.agents {
		cleanupOK = cleanupOK && instance.CleanupReceipt().Passed && !instance.CleanupReceipt().Forced
	}
	processesClean := heldRealPIDGone(dashboardIdentity.PID) && heldRealGroupGone(dashboardIdentity.ProcessGroupID)
	for _, identity := range agentIdentities {
		processesClean = processesClean && heldRealPIDGone(identity.PID) && heldRealGroupGone(identity.ProcessGroupID)
	}
	workspacesClean := true
	for _, root := range workspaceRoots {
		workspacesClean = workspacesClean && heldSessionSetRealWorkspaceGone(root)
	}
	evidenceValue := heldSessionSetRealEvidence{Version: 1, Profile: string(plan.Profile), Seed: "4e5a4841", BaselineCount: baseline.Count, LiveCount: live.Count, ClosedCount: closed.Count, TerminalCount: 4, NATCount: 4, FMCount: 4, AgentOrdinals: []int{1, 2, 3, 4, 5, 6, 7, 8}, ProtocolProved: protocolProved, ExactIDsPresent: true, ExactIDsAbsent: true, PIDStable: true, ResourcesAbsent: resourcesAbsent, ProcessesClean: processesClean, WorkspacesClean: workspacesClean, CleanupOK: cleanupOK}
	for index, instance := range realFixture.agents {
		evidenceValue.AgentSummaries = append(evidenceValue.AgentSummaries, heldSessionSetRealAgentSummary{Ordinal: index + 1, ServerDigest: heldRealDigest(string(rune(realFixture.readiness[index].ServerID))), PATIdentity: realFixture.agentPATs[index].IdentitySeen, PATScopeExact: len(realFixture.agentPATs[index].ServerIDs) == 1 && realFixture.agentPATs[index].ServerIDs[0] == realFixture.readiness[index].ServerID})
		_ = instance
	}
	for index, session := range set.sessions {
		evidenceValue.SessionDigests = append(evidenceValue.SessionDigests, heldRealDigest(streamIDs[index]))
		evidenceValue.SessionSummaries = append(evidenceValue.SessionSummaries, heldSessionSetRealSessionSummary{Ordinal: index + 1, Kind: string(session.Plan().Kind), AgentOrdinal: session.Plan().Agent.Int(), StreamDigest: heldRealDigest(streamIDs[index]), Present: true, Absent: true, Protocol: heldRealSessionProtocolProved(session)})
	}
	require.True(t, cleanupOK && processesClean && workspacesClean)
	require.NoError(t, writeHeldSessionSetRealEvidence("/tmp/nezha-held-real-sessions", evidenceValue))
	_, err = readHeldSessionSetRealEvidence("/tmp/nezha-held-real-sessions")
	require.NoError(t, err)
}

func heldRealSessionProtocolProved(session heldSession) bool {
	switch concrete := session.(type) {
	case *heldTerminalSession:
		return concrete.ProtocolProved()
	case *heldNATSession:
		return concrete.ProtocolProved()
	case *heldLegacyFMSession:
		return concrete.ProtocolProved()
	default:
		return false
	}
}

func heldRealSessionResourcesAbsent(ctx context.Context, fixture *heldSessionSetRealFixture, sessions []heldSession) bool {
	for _, session := range sessions {
		switch concrete := session.(type) {
		case *heldNATSession:
			present, err := heldRealNATProfilePresent(ctx, fixture.dashboard.Clients().REST, concrete.profileID)
			if err != nil || present {
				return false
			}
		case *heldLegacyFMSession:
			rootName := heldLegacyFMRootName.ReplaceAllString(session.Plan().ID.String(), "-")
			if !heldSessionSetRealWorkspaceGone("" + fixture.agents[session.Plan().Agent.Int()-1].WorkspaceRoot() + "/held-fm-" + rootName) {
				return false
			}
		case *heldTerminalSession:
			_ = concrete
		}
	}
	return true
}

func heldRealGroupGone(pgid int) bool {
	if pgid < 1 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return errors.Is(err, syscall.ESRCH)
}
