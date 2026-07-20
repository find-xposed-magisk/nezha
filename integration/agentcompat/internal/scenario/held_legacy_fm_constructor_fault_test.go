//go:build linux

package scenario

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

type heldLegacyFMConstructorFault struct {
	name      string
	failStage string
	wantError error
}

type heldLegacyFMConstructorObservation struct {
	order       []string
	expectation client.IOStreamStateExpectation
}

func TestNewHeldLegacyFMSessionConstructorFaultsRollbackInLIFOOrder(t *testing.T) {
	tests := []heldLegacyFMConstructorFault{
		{name: "create response", failStage: "create", wantError: errHeldLegacyFMConstructorCreate},
		{name: "capability wait", failStage: "wait", wantError: errHeldLegacyFMConstructorWait},
		{name: "response and wait mismatch", failStage: "mismatch", wantError: ErrHeldLegacyFMProtocol},
		{name: "WebSocket dial", failStage: "dial", wantError: errHeldLegacyFMConstructorDial},
		{name: "pump setup", failStage: "pump", wantError: errHeldLegacyFMConstructorPump},
		{name: "list proof", failStage: "proof", wantError: errLegacyFMRemote},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := heldLegacyFMConstructorInput(t)
			observation := &heldLegacyFMConstructorObservation{}
			dependencies := heldLegacyFMConstructorDependencies(input, observation, testCase.failStage)

			_, err := newHeldLegacyFMSessionWithDependencies(context.Background(), input, dependencies)

			require.ErrorIs(t, err, testCase.wantError)
			for _, cleanupError := range []error{errHeldLegacyFMConstructorCancel, errHeldLegacyFMConstructorAbsence, errHeldLegacyFMConstructorUnregister, errHeldLegacyFMConstructorFixture} {
				require.ErrorIs(t, err, cleanupError)
			}
			wantOrder := []string{"cancel", "absence", "unregister", "fixture"}
			if testCase.failStage == "pump" {
				wantOrder = []string{"close", "cancel", "absence", "unregister", "fixture"}
				require.ErrorIs(t, err, errHeldLegacyFMConstructorClose)
			}
			if testCase.failStage == "proof" {
				wantOrder = []string{"pump", "close", "cancel", "absence", "unregister", "fixture"}
				require.ErrorIs(t, err, errHeldLegacyFMConstructorPumpStop)
				require.ErrorIs(t, err, errHeldLegacyFMConstructorClose)
			}
			require.Equal(t, wantOrder, observation.order)
			require.Nil(t, observation.expectation.ExpectedCount)
			expectedAbsent := "stream-legacy-fm"
			if testCase.failStage == "mismatch" {
				expectedAbsent = "different-stream"
			}
			require.Equal(t, expectedAbsent, observation.expectation.AbsentStreamID)
		})
	}
}

var (
	errHeldLegacyFMConstructorCreate     = errors.New("held FM constructor create failed")
	errHeldLegacyFMConstructorWait       = errors.New("held FM constructor wait failed")
	errHeldLegacyFMConstructorDial       = errors.New("held FM constructor dial failed")
	errHeldLegacyFMConstructorPump       = errors.New("held FM constructor pump failed")
	errHeldLegacyFMConstructorClose      = errors.New("held FM constructor close cleanup failed")
	errHeldLegacyFMConstructorPumpStop   = errors.New("held FM constructor pump stop cleanup failed")
	errHeldLegacyFMConstructorCancel     = errors.New("held FM constructor cancel cleanup failed")
	errHeldLegacyFMConstructorAbsence    = errors.New("held FM constructor absence cleanup failed")
	errHeldLegacyFMConstructorUnregister = errors.New("held FM constructor unregister cleanup failed")
	errHeldLegacyFMConstructorFixture    = errors.New("held FM constructor fixture cleanup failed")
)

type heldLegacyFMConstructorConnection struct {
	order *[]string
}

func (connection *heldLegacyFMConstructorConnection) WriteFrame(context.Context, client.Frame) error {
	return nil
}

func (connection *heldLegacyFMConstructorConnection) Close() error {
	*connection.order = append(*connection.order, "close")
	return errHeldLegacyFMConstructorClose
}

type heldLegacyFMConstructorPump struct {
	events chan client.Frame
	order  *[]string
}

func (pump *heldLegacyFMConstructorPump) Events() <-chan client.Frame { return pump.events }
func (pump *heldLegacyFMConstructorPump) Done() <-chan struct{}       { return make(chan struct{}) }
func (pump *heldLegacyFMConstructorPump) Err() error                  { return nil }
func (pump *heldLegacyFMConstructorPump) Stop(context.Context) error {
	*pump.order = append(*pump.order, "pump")
	return errHeldLegacyFMConstructorPumpStop
}

type heldLegacyFMConstructorCapability struct {
	observation *heldLegacyFMConstructorObservation
	streamID    string
	waitErr     error
}

func (capability *heldLegacyFMConstructorCapability) HeaderCapability() client.IOStreamCapability {
	parsed, _ := client.ParseIOStreamCapability("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	return parsed
}

func (capability *heldLegacyFMConstructorCapability) Wait(context.Context) (string, error) {
	return capability.streamID, capability.waitErr
}

func (capability *heldLegacyFMConstructorCapability) Cancel(context.Context) error {
	capability.observation.order = append(capability.observation.order, "cancel")
	return errHeldLegacyFMConstructorCancel
}

func (capability *heldLegacyFMConstructorCapability) Unregister(context.Context) error {
	capability.observation.order = append(capability.observation.order, "unregister")
	return errHeldLegacyFMConstructorUnregister
}

func (capability *heldLegacyFMConstructorCapability) WaitExpectation(_ context.Context, _ *client.Client, _ client.IOStreamState, absent bool) error {
	if absent {
		capability.observation.order = append(capability.observation.order, "absence")
		capability.observation.expectation = client.IOStreamStateExpectation{AbsentStreamID: capability.streamID}
	}
	return errHeldLegacyFMConstructorAbsence
}

func heldLegacyFMConstructorDependencies(input heldLegacyFMInput, observation *heldLegacyFMConstructorObservation, failStage string) heldLegacyFMDependencies {
	listPath := filepath.Join(input.Agent.WorkspaceRoot(), "held-fm-"+input.Plan.ID.String(), "list")
	defaultDependencies := defaultHeldLegacyFMDependencies()
	return heldLegacyFMDependencies{
		RemoveFixture: func(ctx context.Context, workspaceRoot, fixtureRoot string) error {
			observation.order = append(observation.order, "fixture")
			_ = defaultDependencies.RemoveFixture(ctx, workspaceRoot, fixtureRoot)
			return errHeldLegacyFMConstructorFixture
		},
		SnapshotState: func(context.Context, *client.Client) (client.IOStreamState, error) {
			return client.IOStreamState{Count: 4}, nil
		},
		Register: func(context.Context, *client.Client, heldIOStreamCapabilityIdentity) (heldLegacyFMCapabilityHandle, error) {
			capability := &heldLegacyFMConstructorCapability{observation: observation, streamID: "stream-legacy-fm"}
			if failStage == "wait" {
				capability.waitErr = errHeldLegacyFMConstructorWait
			}
			if failStage == "mismatch" {
				capability.streamID = "different-stream"
			}
			return capability, nil
		},
		CreateSession: func(context.Context, *client.Client, uint64, client.IOStreamCapability) (string, error) {
			if failStage == "create" {
				return "", errHeldLegacyFMConstructorCreate
			}
			return "stream-legacy-fm", nil
		},
		DialWebSocket: func(context.Context, *client.Client, string) (heldLegacyFMConnection, error) {
			if failStage == "dial" {
				return nil, errHeldLegacyFMConstructorDial
			}
			return &heldLegacyFMConstructorConnection{order: &observation.order}, nil
		},
		NewPump: func(context.Context, heldLegacyFMConnection, int) (heldLegacyFMPump, error) {
			if failStage == "pump" {
				return nil, errHeldLegacyFMConstructorPump
			}
			frame := heldLegacyFMListFrame(listPath, "entry.txt", false)
			if failStage == "proof" {
				frame = []byte("NERRdenied")
			}
			events := make(chan client.Frame, 1)
			events <- client.Frame{Type: client.FrameBinary, Payload: frame}
			return &heldLegacyFMConstructorPump{events: events, order: &observation.order}, nil
		},
		WaitForState: func(ctx context.Context, stateClient *client.Client, baseline client.IOStreamState, capability heldLegacyFMCapabilityHandle, absent bool) error {
			return capability.WaitExpectation(ctx, stateClient, baseline, absent)
		},
	}
}

func heldLegacyFMConstructorInput(t *testing.T) heldLegacyFMInput {
	t.Helper()
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000501")
	return heldLegacyFMInput{
		Dashboard: &dashboard.Dashboard{},
		PATClient: &client.Client{},
		Agent:     agentInstance,
		Readiness: completeHeldReadiness(agentInstance.UUID()),
		Plan:      heldFMTestPlan(t, StressSessionFM),
	}
}
