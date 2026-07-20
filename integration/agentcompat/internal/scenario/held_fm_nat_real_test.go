//go:build linux && agentcompat

package scenario

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

type heldRealNATProfile struct {
	ID uint64 `json:"id"`
}

type heldRealFixture struct {
	dashboard    *dashboard.Dashboard
	agent        *agent.Agent
	dashboardPID int
	agentPID     int
	closed       bool
}

func (fixture *heldRealFixture) Close(ctx context.Context, sessionClosed, exactStreamGone, ownedResourceGone bool) (heldRealCleanup, error) {
	if fixture.closed {
		return heldRealCleanup{}, nil
	}
	fixture.closed = true
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	agentErr := fixture.agent.Stop(cleanupContext)
	dashboardErr := fixture.dashboard.Stop(cleanupContext)
	cleanup := heldRealCleanup{Agent: fixture.agent.CleanupReceipt(), Dashboard: fixture.dashboard.CleanupReceipt(), SessionClosed: sessionClosed, ExactStreamGone: exactStreamGone, OwnedResourceGone: ownedResourceGone, AgentPIDGone: heldRealPIDGone(fixture.agentPID), DashboardPIDGone: heldRealPIDGone(fixture.dashboardPID)}
	return cleanup, errors.Join(agentErr, dashboardErr)
}

func requireHeldRealSources(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTCOMPAT_NEZHA_SOURCE") == "" || os.Getenv("AGENTCOMPAT_AGENT_SOURCE") == "" {
		t.Skip("set AGENTCOMPAT_NEZHA_SOURCE and AGENTCOMPAT_AGENT_SOURCE")
	}
}

func TestHeldLegacyFMSessionUsesExistingDashboardAndAgent(t *testing.T) {
	// Given
	requireHeldRealSources(t)
	paths, err := contract.NewPaths(os.Getenv("AGENTCOMPAT_NEZHA_SOURCE"), os.Getenv("AGENTCOMPAT_AGENT_SOURCE"), t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	realFixture, readiness, patClient := startHeldRealFixture(t, ctx, paths, "00000000-0000-0000-0000-000000000118", "held-fm")
	dashboardInstance, agentInstance := realFixture.dashboard, realFixture.agent
	t.Cleanup(func() { _, _ = realFixture.Close(context.Background(), false, false, false) })
	plan := heldFMTestPlan(t, StressSessionFM)
	plan.ID, err = NewStressSessionID(fmt.Sprintf("held-fm-real-%d", time.Now().UnixNano()))
	require.NoError(t, err)
	baseline, err := patClient.IOStreamState(ctx)
	require.NoError(t, err)
	dashboardPID, agentPID := dashboardInstance.PID(), agentInstance.PID()

	// When
	sessionCtx, sessionCancel := context.WithTimeout(ctx, 45*time.Second)
	defer sessionCancel()
	session, err := newHeldLegacyFMSession(sessionCtx, heldLegacyFMInput{Dashboard: dashboardInstance, PATClient: patClient, Agent: agentInstance, Readiness: readiness, Plan: plan})
	require.NoError(t, err)
	require.NoError(t, session.WaitLive(ctx))
	require.True(t, session.ProtocolProved())
	streamID, present := session.IOStreamID()
	require.True(t, present)
	fixtureRoot := filepath.Join(agentInstance.WorkspaceRoot(), "held-fm-"+heldLegacyFMRootName.ReplaceAllString(plan.ID.String(), "-"))
	_, err = os.Stat(fixtureRoot)
	require.NoError(t, err)
	live, err := patClient.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count + 1), PresentStreamID: streamID})
	require.NoError(t, err)
	require.Equal(t, baseline.Count+1, live.Count)
	require.Equal(t, dashboardPID, dashboardInstance.PID())
	require.Equal(t, agentPID, agentInstance.PID())
	dashboardPIDUnchanged := dashboardPID == dashboardInstance.PID()
	agentPIDUnchanged := agentPID == agentInstance.PID()

	// Then
	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.WaitClosed(ctx))
	closed, err := patClient.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count), AbsentStreamID: streamID})
	require.NoError(t, err)
	require.Equal(t, baseline.Count, closed.Count)
	require.NotEmpty(t, streamID)
	require.Equal(t, dashboardPID, dashboardInstance.PID())
	require.Equal(t, agentPID, agentInstance.PID())
	require.NotZero(t, dashboardPID)
	require.NotZero(t, agentPID)
	_, err = os.Stat(fixtureRoot)
	require.ErrorIs(t, err, os.ErrNotExist)
	cleanup, cleanupErr := realFixture.Close(ctx, true, closed.Count == baseline.Count, errors.Is(err, os.ErrNotExist))
	require.NoError(t, cleanupErr)
	require.True(t, heldRealCleanupOK(cleanup))
	require.NoError(t, writeHeldRealEvidence("file-manager", heldRealEvidence{Kind: "file-manager", BaselineCount: baseline.Count, LiveCount: live.Count, ClosedCount: closed.Count, ExactIDPresent: true, ExactIDAbsent: true, ProtocolProved: session.ProtocolProved(), DashboardPIDUnchanged: dashboardPIDUnchanged, AgentPIDUnchanged: agentPIDUnchanged, CleanupOK: heldRealCleanupOK(cleanup)}))
}

func TestHeldNATSessionUsesExistingDashboardAndAgent(t *testing.T) {
	// Given
	requireHeldRealSources(t)
	paths, err := contract.NewPaths(os.Getenv("AGENTCOMPAT_NEZHA_SOURCE"), os.Getenv("AGENTCOMPAT_AGENT_SOURCE"), t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	realFixture, readiness, patClient := startHeldRealFixture(t, ctx, paths, "00000000-0000-0000-0000-000000000119", "held-nat")
	dashboardInstance, agentInstance := realFixture.dashboard, realFixture.agent
	t.Cleanup(func() { _, _ = realFixture.Close(context.Background(), false, false, false) })
	plan := heldNATTestPlan(t)
	plan.ID, err = NewStressSessionID(fmt.Sprintf("held-nat-real-%d", time.Now().UnixNano()))
	require.NoError(t, err)
	baseline, err := patClient.IOStreamState(ctx)
	require.NoError(t, err)
	dashboardPID, agentPID := dashboardInstance.PID(), agentInstance.PID()

	// When
	sessionCtx, sessionCancel := context.WithTimeout(ctx, 45*time.Second)
	defer sessionCancel()
	session, err := newHeldNATSession(sessionCtx, heldNATInput{Dashboard: dashboardInstance, PATClient: patClient, Agent: agentInstance, Readiness: readiness, Plan: plan})
	require.NoError(t, err)
	require.NoError(t, session.WaitLive(ctx))
	require.True(t, session.ProtocolProved())
	present, err := heldRealNATProfilePresent(ctx, dashboardInstance.Clients().REST, session.profileID)
	require.NoError(t, err)
	require.True(t, present)
	streamID, present := session.IOStreamID()
	require.True(t, present)
	live, err := patClient.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count + 1), PresentStreamID: streamID})
	require.NoError(t, err)
	require.Equal(t, baseline.Count+1, live.Count)
	require.Equal(t, dashboardPID, dashboardInstance.PID())
	require.Equal(t, agentPID, agentInstance.PID())
	dashboardPIDUnchanged := dashboardPID == dashboardInstance.PID()
	agentPIDUnchanged := agentPID == agentInstance.PID()
	require.Equal(t, http.MethodPatch, session.observed.Method)
	require.Equal(t, "/held/"+plan.ID.String(), session.observed.Path)
	domain, domainErr := heldNATDomain(plan.ID.String())
	require.NoError(t, domainErr)
	require.Equal(t, domain, session.observed.Host)
	require.Equal(t, plan.ID.String(), session.observed.HeaderValue)
	require.Equal(t, "held-body-"+plan.ID.String(), string(session.observed.Body))
	require.False(t, session.observed.SensitiveHeadersPresent)

	// Then
	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.WaitClosed(ctx))
	closed, err := patClient.WaitForIOStreamState(ctx, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(baseline.Count), AbsentStreamID: streamID})
	require.NoError(t, err)
	require.Equal(t, baseline.Count, closed.Count)
	require.NotEmpty(t, streamID)
	require.Equal(t, dashboardPID, dashboardInstance.PID())
	require.Equal(t, agentPID, agentInstance.PID())
	require.NotZero(t, dashboardPID)
	require.NotZero(t, agentPID)
	require.False(t, session.observed.SensitiveHeadersPresent)
	present, err = heldRealNATProfilePresent(ctx, dashboardInstance.Clients().REST, session.profileID)
	require.NoError(t, err)
	require.False(t, present)
	cleanup, cleanupErr := realFixture.Close(ctx, true, closed.Count == baseline.Count, !present)
	require.NoError(t, cleanupErr)
	require.True(t, heldRealCleanupOK(cleanup))
	require.NoError(t, writeHeldRealEvidence("nat", heldRealEvidence{Kind: "nat", BaselineCount: baseline.Count, LiveCount: live.Count, ClosedCount: closed.Count, ExactIDPresent: true, ExactIDAbsent: true, ProtocolProved: session.ProtocolProved(), SensitiveHeadersPresent: session.observed.SensitiveHeadersPresent, DashboardPIDUnchanged: dashboardPIDUnchanged, AgentPIDUnchanged: agentPIDUnchanged, CleanupOK: heldRealCleanupOK(cleanup)}))
}

func heldRealNATProfilePresent(ctx context.Context, admin *client.Client, profileID uint64) (bool, error) {
	return heldRealNATProfilePresentWithQuery(ctx, profileID, func(queryContext context.Context) ([]heldRealNATProfile, error) {
		return client.DoREST[struct{}, []heldRealNATProfile](queryContext, admin, client.RESTRequest[struct{}]{Method: http.MethodGet, Path: "/api/v1/nat"})
	})
}

func heldRealNATProfilePresentWithQuery(ctx context.Context, profileID uint64, query func(context.Context) ([]heldRealNATProfile, error)) (bool, error) {
	profiles, err := query(ctx)
	if err != nil {
		return false, err
	}
	for _, profile := range profiles {
		if profile.ID == profileID {
			return true, nil
		}
	}
	return false, nil
}

func startHeldRealFixture(t *testing.T, ctx context.Context, paths contract.Paths, uuid, name string) (*heldRealFixture, agent.Readiness, *client.Client) {
	t.Helper()
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: paths.NezhaSource().String(), ReceiptGate: true})
	require.NoError(t, err)
	agentInstance, err := agent.Start(ctx, agent.AgentStartConfig{SourceDir: paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(), Secret: dashboardInstance.AgentSecret(), UUID: uuid})
	require.NoError(t, err)
	require.NoError(t, dashboardInstance.WaitForReceiptAccepted(ctx))
	require.NoError(t, dashboardInstance.ReleaseReceipt(ctx))
	readiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	require.NoError(t, err)
	patClient, err := createTerminalPATClient(ctx, dashboardInstance, name, []string{"nezha:*"}, []uint64{readiness.ServerID})
	require.NoError(t, err)
	return &heldRealFixture{dashboard: dashboardInstance, agent: agentInstance, dashboardPID: dashboardInstance.PID(), agentPID: agentInstance.PID()}, readiness, patClient
}

func heldRealPIDGone(pid int) bool {
	if pid < 1 {
		return false
	}
	_, err := os.Stat(filepath.Join("/proc", fmt.Sprint(pid)))
	return errors.Is(err, os.ErrNotExist)
}
