//go:build linux

package scenario

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

type heldNATConstructorFault struct {
	name          string
	failStage     string
	wantOrder     []string
	wantStreamID  string
	wantAbsentID  string
	wantPresent   bool
	wantOriginal  error
	wantRequest   bool
	wantProofData fixture.NATEchoRecord
	wantBaseline  int
}

func TestHeldNATConstructorFaultsRollbackRegisteredActions(t *testing.T) {
	tests := []heldNATConstructorFault{
		{name: "request start", failStage: "start", wantOrder: []string{"cancel", "absence", "unregister", "profile", "backend"}, wantAbsentID: "", wantBaseline: 9, wantOriginal: errHeldNATConstructorRequestStart},
		{name: "request observation", failStage: "observe", wantOrder: []string{"request", "cancel", "absence", "unregister", "profile", "backend"}, wantAbsentID: "", wantBaseline: 9, wantOriginal: errHeldNATConstructorObservation, wantRequest: true},
		{name: "sensitive header proof", failStage: "proof", wantOrder: []string{"request", "cancel", "absence", "unregister", "profile", "backend"}, wantAbsentID: "", wantBaseline: 9, wantOriginal: errHeldNATConstructorProof, wantRequest: true, wantProofData: fixture.NATEchoRecord{SensitiveHeadersPresent: true}},
		{name: "capability wait", failStage: "wait", wantOrder: []string{"request", "cancel", "absence", "unregister", "profile", "backend"}, wantAbsentID: "", wantBaseline: 9, wantOriginal: errHeldNATConstructorWait, wantRequest: true},
		{name: "exact stream ID assignment", failStage: "set-id", wantOrder: []string{"request", "cancel", "absence", "unregister", "profile", "backend"}, wantStreamID: "stream-401", wantAbsentID: "stream-401", wantBaseline: 9, wantOriginal: errHeldNATConstructorSetID, wantRequest: true},
		{name: "present expectation", failStage: "present", wantOrder: []string{"request", "cancel", "absence", "unregister", "profile", "backend"}, wantStreamID: "stream-402", wantAbsentID: "stream-402", wantBaseline: 9, wantOriginal: errHeldNATConstructorPresent, wantRequest: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var order []string
			var observedAbsentID string
			var observedPresent bool
			var observedBaseline int
			dependencies := constructorFaultDependencies(&order, &observedAbsentID, &observedPresent, &observedBaseline, test)
			input := heldNATConstructorInput(t)

			_, err := newHeldNATSessionWithDependencies(context.Background(), input, dependencies)

			if !errors.Is(err, test.wantOriginal) || !errors.Is(err, errHeldNATConstructorCancel) || !errors.Is(err, errHeldNATConstructorAbsence) {
				t.Fatalf("error=%v, want original plus cancel and absence failures", err)
			}
			if !errors.Is(err, errHeldNATConstructorUnregister) || !errors.Is(err, errHeldNATConstructorProfile) || !errors.Is(err, errHeldNATConstructorBackend) {
				t.Fatalf("error=%v, want all later cleanup failures", err)
			}
			if test.wantRequest && !errors.Is(err, errHeldNATConstructorRequestClose) {
				t.Fatalf("error=%v, want request close failure", err)
			}
			if !reflect.DeepEqual(order, test.wantOrder) {
				t.Fatalf("cleanup order=%v, want %v", order, test.wantOrder)
			}
			if observedAbsentID != test.wantAbsentID || observedPresent != test.wantPresent {
				t.Fatalf("absence expectation stream=%q present=%t, want stream=%q present=%t", observedAbsentID, observedPresent, test.wantAbsentID, test.wantPresent)
			}
			if observedBaseline != test.wantBaseline {
				t.Fatalf("absence baseline=%d, want %d", observedBaseline, test.wantBaseline)
			}
		})
	}
}

var (
	errHeldNATConstructorRequestStart = errors.New("constructor request start failed")
	errHeldNATConstructorObservation  = errors.New("constructor request observation failed")
	errHeldNATConstructorProof        = errors.New("constructor request proof failed")
	errHeldNATConstructorWait         = errors.New("constructor capability wait failed")
	errHeldNATConstructorSetID        = errors.New("constructor stream ID assignment failed")
	errHeldNATConstructorPresent      = errors.New("constructor present expectation failed")
	errHeldNATConstructorCancel       = errors.New("constructor cancel cleanup failed")
	errHeldNATConstructorAbsence      = errors.New("constructor absence cleanup failed")
	errHeldNATConstructorUnregister   = errors.New("constructor unregister cleanup failed")
	errHeldNATConstructorProfile      = errors.New("constructor profile cleanup failed")
	errHeldNATConstructorBackend      = errors.New("constructor backend cleanup failed")
)

func constructorFaultDependencies(order *[]string, observedAbsentID *string, observedPresent *bool, observedBaseline *int, test heldNATConstructorFault) heldNATDependencies {
	return heldNATDependencies{
		snapshotState: func(context.Context, *client.Client) (client.IOStreamState, error) {
			return client.IOStreamState{Count: 9}, nil
		},
		createProfile: func(context.Context, *dashboard.Dashboard, *fixture.NATHoldBackend, uint64, string, string) (uint64, error) {
			return 401, nil
		},
		deleteProfile: func(context.Context, *dashboard.Dashboard, uint64) error {
			*order = append(*order, "profile")
			return errHeldNATConstructorProfile
		},
		register: func(context.Context, *client.Client, heldIOStreamCapabilityIdentity) (*heldIOStreamCapability, error) {
			return &heldIOStreamCapability{}, nil
		},
		startRequest: func(context.Context, string, string, string, client.IOStreamCapability) (*heldNATRequest, error) {
			if test.failStage == "start" {
				return nil, errHeldNATConstructorRequestStart
			}
			return &heldNATRequest{connection: closeErrorConn{err: errHeldNATConstructorRequestClose}, result: closedResult()}, nil
		},
		waitRequestObserved: func(context.Context, *fixture.NATHoldBackend) error { return nil },
		closeBackend: func(backend *fixture.NATHoldBackend) error {
			*order = append(*order, "backend")
			return errors.Join(backend.Close(), errHeldNATConstructorBackend)
		},
		closeRequest: func(request *heldNATRequest) error {
			*order = append(*order, "request")
			return errors.Join(request.close(), errHeldNATConstructorRequestClose)
		},
		cancelCapability: func(context.Context, *heldIOStreamCapability) error {
			*order = append(*order, "cancel")
			return errHeldNATConstructorCancel
		},
		unregisterCapability: func(context.Context, *heldIOStreamCapability) error {
			*order = append(*order, "unregister")
			return errHeldNATConstructorUnregister
		},
		waitExpectation: func(_ context.Context, capability *heldIOStreamCapability, _ *client.Client, baseline client.IOStreamState, absent bool) error {
			*observedBaseline = baseline.Count
			if absent {
				*order = append(*order, "absence")
			} else if test.failStage == "present" {
				return errHeldNATConstructorPresent
			}
			if absent {
				*observedAbsentID = capability.streamID
				return errHeldNATConstructorAbsence
			}
			*observedPresent = true
			return nil
		},
		waitRequest: func(context.Context, *fixture.NATHoldBackend) (fixture.NATEchoRecord, error) {
			if test.failStage == "observe" {
				return fixture.NATEchoRecord{}, errHeldNATConstructorObservation
			}
			return test.wantProofData, nil
		},
		proveRequest: func(fixture.NATEchoRecord, string, string) error {
			if test.failStage == "proof" {
				return errHeldNATConstructorProof
			}
			return nil
		},
		waitCapability: func(_ context.Context, capability *heldIOStreamCapability) (string, error) {
			if test.failStage == "wait" {
				return "", errHeldNATConstructorWait
			}
			capability.streamID = test.wantStreamID
			return test.wantStreamID, nil
		},
		setStreamID: func(_ *heldSessionLifecycle, streamID string) error {
			if test.failStage == "set-id" {
				return errHeldNATConstructorSetID
			}
			return nil
		},
	}
}

var errHeldNATConstructorRequestClose = errors.New("constructor request close failed")

func closedResult() chan error {
	result := make(chan error, 1)
	result <- net.ErrClosed
	return result
}

func heldNATConstructorInput(t *testing.T) heldNATInput {
	t.Helper()
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000402")
	return heldNATInput{
		Dashboard: &dashboard.Dashboard{},
		PATClient: &client.Client{},
		Agent:     agentInstance,
		Readiness: completeHeldReadiness(agentInstance.UUID()),
		Plan:      heldNATTestPlan(t),
	}
}
