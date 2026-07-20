//go:build linux && agentcompat

package scenario

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestHeldTerminalSessionUsesExistingDashboardAndAgent(t *testing.T) {
	requireHeldRealSources(t)
	paths, err := contract.NewPaths(os.Getenv("AGENTCOMPAT_NEZHA_SOURCE"), os.Getenv("AGENTCOMPAT_AGENT_SOURCE"), t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	realFixture, readiness, patClient := startHeldRealFixture(t, ctx, paths, "00000000-0000-0000-0000-000000000118", "held-terminal")
	dashboardInstance, agentInstance := realFixture.dashboard, realFixture.agent
	t.Cleanup(func() { _, _ = realFixture.Close(context.Background(), false, false, false) })
	plan := heldTestPlan(t)
	plan.ID, err = NewStressSessionID(fmt.Sprintf("held-terminal-real-%d", time.Now().UnixNano()))
	require.NoError(t, err)
	baseline, err := patClient.IOStreamState(ctx)
	require.NoError(t, err)
	dashboardPID, agentPID := dashboardInstance.PID(), agentInstance.PID()

	sessionCtx, sessionCancel := context.WithTimeout(ctx, 45*time.Second)
	defer sessionCancel()
	session, err := newHeldTerminalSession(sessionCtx, heldTerminalInput{Dashboard: dashboardInstance, PATClient: patClient, Agent: agentInstance, Readiness: readiness, Plan: plan})
	require.NoError(t, err)
	require.NoError(t, session.WaitLive(ctx))
	streamID, present := session.IOStreamID()
	require.True(t, present)
	live, err := patClient.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count + 1), PresentStreamID: streamID})
	require.NoError(t, err)
	require.Equal(t, baseline.Count+1, live.Count)
	require.Equal(t, dashboardPID, dashboardInstance.PID())
	require.Equal(t, agentPID, agentInstance.PID())

	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.WaitClosed(ctx))
	closed, err := patClient.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count), AbsentStreamID: streamID})
	require.NoError(t, err)
	require.Equal(t, baseline.Count, closed.Count)
	cleanup, cleanupErr := realFixture.Close(ctx, true, closed.Count == baseline.Count, true)
	require.NoError(t, cleanupErr)
	require.True(t, heldRealCleanupOK(cleanup))
	require.NoError(t, writeHeldRealEvidence("terminal", heldRealEvidence{Kind: "terminal", BaselineCount: baseline.Count, LiveCount: live.Count, ClosedCount: closed.Count, ExactIDPresent: true, ExactIDAbsent: true, ProtocolProved: session.ProtocolProved(), DashboardPIDUnchanged: dashboardPID == dashboardInstance.PID(), AgentPIDUnchanged: agentPID == agentInstance.PID(), CleanupOK: heldRealCleanupOK(cleanup)}))
}
