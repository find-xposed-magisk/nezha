//go:build linux

package scenario

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

func TestHeldNATSessionRejectsInvalidInput(t *testing.T) {
	plan := heldNATTestPlan(t)
	patClient, err := client.New(client.Config{BaseURL: "http://127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = newHeldNATSession(context.Background(), heldNATInput{PATClient: patClient, Plan: plan})
	if !errors.Is(err, ErrInvalidHeldNATSession) {
		t.Fatalf("error=%v", err)
	}
}

func TestHeldNATSessionRejectsNilPATBeforeRemoteMutation(t *testing.T) {
	plan := heldNATTestPlan(t)

	_, err := newHeldNATSession(context.Background(), heldNATInput{Plan: plan})

	if !errors.Is(err, ErrInvalidHeldPATClient) {
		t.Fatalf("error=%v, want ErrInvalidHeldPATClient before other validation", err)
	}
}

func TestHeldNATSessionCloseBeforeLiveRetainsLifecycleError(t *testing.T) {
	session := newTestHeldNATSession(t)
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.WaitLive(context.Background()); !errors.Is(err, ErrHeldSessionClosedBeforeLive) {
		t.Fatalf("WaitLive=%v", err)
	}
	if err := session.WaitClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHeldNATSessionCanceledWaiterDoesNotCancelOwner(t *testing.T) {
	session := newTestHeldNATSession(t)
	if err := session.lifecycle.markLive(nil); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close=%v", err)
	}
	if err := session.WaitClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHeldNATProofRejectsSensitiveHeaders(t *testing.T) {
	observed := fixture.NATEchoRecord{Method: http.MethodPatch, Path: "/held/held-nat", Host: "held-nat.agentcompat-nat.invalid", HeaderValue: "held-nat", Body: []byte("held-body-held-nat"), SensitiveHeadersPresent: true}

	err := proveHeldNATRequest(observed, observed.Host, "held-nat")

	if err == nil {
		t.Fatal("proof accepted sensitive backend headers")
	}
}

func TestHeldNATInputRejectsNilPATBeforeReadinessOrPlan(t *testing.T) {
	_, err := newHeldNATSession(context.Background(), heldNATInput{PATClient: nil})

	if !errors.Is(err, ErrInvalidHeldPATClient) {
		t.Fatalf("error=%v, want PAT validation before readiness and plan validation", err)
	}
}

func TestHeldNATCleanupOrderIsLIFOForRequiredResources(t *testing.T) {
	stack := newHeldCleanupStack()
	var order []string
	for _, name := range []string{"baseline", "backend", "profile", "unregister", "absence", "cancel", "request"} {
		name := name
		if err := stack.Push(heldCleanupAction{name: name, cleanup: func(context.Context) error {
			order = append(order, name)
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := stack.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"request", "cancel", "absence", "unregister", "profile", "backend", "baseline"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("cleanup order=%v, want %v", order, want)
	}
}

func TestHeldNATRequestCloseRetainsConnectionError(t *testing.T) {
	closeFailure := errors.New("request connection close failed")
	request := &heldNATRequest{connection: closeErrorConn{err: closeFailure}, result: make(chan error, 1)}

	if err := request.close(); !errors.Is(err, closeFailure) {
		t.Fatalf("first close error=%v, want %v", err, closeFailure)
	}
	if err := request.close(); !errors.Is(err, closeFailure) {
		t.Fatalf("repeated close error=%v, want %v", err, closeFailure)
	}
}

func TestHeldNATSessionConcurrentCloseRetainsOneCleanupResult(t *testing.T) {
	session := newTestHeldNATSession(t)
	cleanupFailure := errors.New("NAT cleanup failed")
	if err := session.cleanup.Push(heldCleanupAction{name: "request", cleanup: func(context.Context) error { return cleanupFailure }}); err != nil {
		t.Fatal(err)
	}
	if err := session.lifecycle.markLive(nil); err != nil {
		t.Fatal(err)
	}

	const callers = 8
	errorsSeen := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			errorsSeen <- session.Close(context.Background())
		}()
	}
	group.Wait()
	for range callers {
		if err := <-errorsSeen; !errors.Is(err, cleanupFailure) {
			t.Fatalf("Close error=%v, want %v", err, cleanupFailure)
		}
	}
}

func TestHeldNATRollbackJoinsOriginalAndCleanupFailures(t *testing.T) {
	session := newTestHeldNATSession(t)
	original := errors.New("constructor failure")
	rollbackFailure := errors.New("rollback failure")
	if err := session.cleanup.Push(heldCleanupAction{name: "backend", cleanup: func(context.Context) error { return rollbackFailure }}); err != nil {
		t.Fatal(err)
	}

	err := rollbackHeldNAT(session, original)

	if !errors.Is(err, original) || !errors.Is(err, rollbackFailure) {
		t.Fatalf("joined error=%v, want original and rollback failures", err)
	}
}

func TestHeldNATConstructorRegistrationFailureRollsBackProfileBeforeBackend(t *testing.T) {
	const profileID = uint64(77)
	registrationFailure := errors.New("capability registration failed")
	profileDeleteFailure := errors.New("profile deletion failed")
	var cleanupOrder []string

	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000401")
	plan := heldNATTestPlan(t)
	plan.ID, _ = NewStressSessionID("constructor-rollback")
	input := heldNATInput{
		Dashboard: &dashboard.Dashboard{},
		PATClient: &client.Client{},
		Agent:     agentInstance,
		Readiness: completeHeldReadiness(agentInstance.UUID()),
		Plan:      plan,
	}
	dependencies := defaultHeldNATDependencies()
	dependencies.snapshotState = func(context.Context, *client.Client) (client.IOStreamState, error) {
		return client.IOStreamState{}, nil
	}
	dependencies.createProfile = func(context.Context, *dashboard.Dashboard, *fixture.NATHoldBackend, uint64, string, string) (uint64, error) {
		return profileID, nil
	}
	dependencies.deleteProfile = func(context.Context, *dashboard.Dashboard, uint64) error {
		cleanupOrder = append(cleanupOrder, "profile")
		return profileDeleteFailure
	}
	dependencies.register = func(context.Context, *client.Client, heldIOStreamCapabilityIdentity) (*heldIOStreamCapability, error) {
		return nil, registrationFailure
	}

	_, err := newHeldNATSessionWithDependencies(context.Background(), input, dependencies)

	if !errors.Is(err, registrationFailure) || !errors.Is(err, profileDeleteFailure) {
		t.Fatalf("constructor error=%v, want registration and profile cleanup failures", err)
	}
	if !reflect.DeepEqual(cleanupOrder, []string{"profile"}) {
		t.Fatalf("cleanup order=%v, want profile before backend cleanup", cleanupOrder)
	}
}

type closeErrorConn struct{ err error }

func (connection closeErrorConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (connection closeErrorConn) Write([]byte) (int, error)        { return 0, net.ErrClosed }
func (connection closeErrorConn) Close() error                     { return connection.err }
func (connection closeErrorConn) LocalAddr() net.Addr              { return heldNATTestAddr{} }
func (connection closeErrorConn) RemoteAddr() net.Addr             { return heldNATTestAddr{} }
func (connection closeErrorConn) SetDeadline(time.Time) error      { return nil }
func (connection closeErrorConn) SetReadDeadline(time.Time) error  { return nil }
func (connection closeErrorConn) SetWriteDeadline(time.Time) error { return nil }

type heldNATTestAddr struct{}

func (heldNATTestAddr) Network() string { return "held-nat-test" }
func (heldNATTestAddr) String() string  { return "held-nat-test" }

func heldNATTestPlan(t *testing.T) StressSessionPlan {
	t.Helper()
	id, err := NewStressSessionID("held-nat")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := NewStressAgentOrdinal(1)
	if err != nil {
		t.Fatal(err)
	}
	return StressSessionPlan{ID: id, Kind: StressSessionNAT, Ordinal: 1, Agent: agent}
}

func newTestHeldNATSession(t *testing.T) *heldNATSession {
	t.Helper()
	lifecycle, err := newHeldSessionLifecycle(context.Background(), heldNATTestPlan(t), "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return &heldNATSession{lifecycle: lifecycle, cleanup: newHeldCleanupStack()}
}
