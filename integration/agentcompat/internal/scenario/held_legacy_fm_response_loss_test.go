//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

var (
	errHeldLegacyFMCreateRejected     = errors.New("held FM create rejected")
	errHeldLegacyFMResponseLost       = errors.New("held FM response lost")
	errHeldLegacyFMCapabilityWaitLost = errors.New("held FM capability wait lost")
)

type heldLegacyFMResponseLossCase struct {
	name             string
	createError      error
	waitStreamID     string
	waitError        error
	wantAbsentStream string
	wantCreateError  error
	wantWaitError    error
}

func TestNewHeldLegacyFMSessionRecoversCreateResponseLossForExactCleanup(t *testing.T) {
	tests := []heldLegacyFMResponseLossCase{
		{
			name:            "ordinary rejection",
			createError:     errHeldLegacyFMCreateRejected,
			waitError:       client.ErrIOStreamCapabilityUnavailable,
			wantCreateError: errHeldLegacyFMCreateRejected,
		},
		{
			name:             "response loss after stream creation",
			createError:      errHeldLegacyFMResponseLost,
			waitStreamID:     "stream-response-lost",
			wantAbsentStream: "stream-response-lost",
			wantCreateError:  errHeldLegacyFMResponseLost,
		},
		{
			name:            "create and capability wait failure",
			createError:     errHeldLegacyFMResponseLost,
			waitError:       errHeldLegacyFMCapabilityWaitLost,
			wantCreateError: errHeldLegacyFMResponseLost,
			wantWaitError:   errHeldLegacyFMCapabilityWaitLost,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := heldLegacyFMResponseLossInput(t)
			fixture := newHeldLegacyFMResponseLossFixture(testCase)
			dependencies := heldLegacyFMResponseLossDependencies(input, fixture)

			_, err := newHeldLegacyFMSessionWithDependencies(context.Background(), input, dependencies)

			require.ErrorIs(t, err, testCase.wantCreateError)
			if testCase.wantWaitError != nil {
				require.ErrorIs(t, err, testCase.wantWaitError)
			}
			require.Equal(t, 1, fixture.waitCalls)
			require.Equal(t, testCase.wantAbsentStream, fixture.absenceExpectation.AbsentStreamID)
			require.Nil(t, fixture.absenceExpectation.ExpectedCount)
			require.Equal(t, []string{"cancel", "absence", "unregister", "fixture"}, fixture.order)
		})
	}
}

type heldLegacyFMResponseLossFixture struct {
	caseData           heldLegacyFMResponseLossCase
	order              []string
	waitCalls          int
	absenceExpectation client.IOStreamStateExpectation
}

func newHeldLegacyFMResponseLossFixture(caseData heldLegacyFMResponseLossCase) *heldLegacyFMResponseLossFixture {
	return &heldLegacyFMResponseLossFixture{caseData: caseData}
}

type heldLegacyFMResponseLossCapability struct {
	fixture  *heldLegacyFMResponseLossFixture
	waitOnce bool
	streamID string
	waitErr  error
}

func (capability *heldLegacyFMResponseLossCapability) HeaderCapability() client.IOStreamCapability {
	parsed, _ := client.ParseIOStreamCapability("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	return parsed
}

func (capability *heldLegacyFMResponseLossCapability) Wait(context.Context) (string, error) {
	if capability.waitOnce {
		return capability.streamID, capability.waitErr
	}
	capability.waitOnce = true
	capability.fixture.waitCalls++
	capability.streamID = capability.fixture.caseData.waitStreamID
	capability.waitErr = capability.fixture.caseData.waitError
	return capability.streamID, capability.waitErr
}

func (capability *heldLegacyFMResponseLossCapability) Cancel(context.Context) error {
	capability.fixture.order = append(capability.fixture.order, "cancel")
	return nil
}

func (capability *heldLegacyFMResponseLossCapability) Unregister(context.Context) error {
	capability.fixture.order = append(capability.fixture.order, "unregister")
	return nil
}

func (capability *heldLegacyFMResponseLossCapability) WaitExpectation(_ context.Context, _ *client.Client, _ client.IOStreamState, absent bool) error {
	if absent {
		capability.fixture.order = append(capability.fixture.order, "absence")
		streamID := capability.streamID
		capability.fixture.absenceExpectation = client.IOStreamStateExpectation{AbsentStreamID: streamID}
	}
	return nil
}

func heldLegacyFMResponseLossDependencies(input heldLegacyFMInput, fixture *heldLegacyFMResponseLossFixture) heldLegacyFMDependencies {
	defaults := defaultHeldLegacyFMDependencies()
	return heldLegacyFMDependencies{
		SnapshotState: func(context.Context, *client.Client) (client.IOStreamState, error) {
			return client.IOStreamState{Count: 7}, nil
		},
		Register: func(context.Context, *client.Client, heldIOStreamCapabilityIdentity) (heldLegacyFMCapabilityHandle, error) {
			return &heldLegacyFMResponseLossCapability{fixture: fixture}, nil
		},
		CreateSession: func(context.Context, *client.Client, uint64, client.IOStreamCapability) (string, error) {
			return "", fixture.caseData.createError
		},
		WaitForState: func(ctx context.Context, stateClient *client.Client, baseline client.IOStreamState, capability heldLegacyFMCapabilityHandle, absent bool) error {
			return capability.WaitExpectation(ctx, stateClient, baseline, absent)
		},
		RemoveFixture: func(ctx context.Context, workspaceRoot, fixtureRoot string) error {
			fixture.order = append(fixture.order, "fixture")
			return defaults.RemoveFixture(ctx, workspaceRoot, fixtureRoot)
		},
	}
}

func heldLegacyFMResponseLossInput(t *testing.T) heldLegacyFMInput {
	t.Helper()
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000601")
	return heldLegacyFMInput{
		Dashboard: &dashboard.Dashboard{},
		PATClient: &client.Client{},
		Agent:     agentInstance,
		Readiness: completeHeldReadiness(agentInstance.UUID()),
		Plan:      heldFMTestPlan(t, StressSessionFM),
	}
}
