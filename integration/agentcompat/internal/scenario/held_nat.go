//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

var ErrInvalidHeldNATSession = errors.New("held NAT session input is invalid")

type heldNATInput struct {
	Dashboard       *dashboard.Dashboard
	PATClient       *client.Client
	Agent           *agent.Agent
	Readiness       agent.Readiness
	Plan            StressSessionPlan
	LifetimeContext context.Context
}

type heldNATSession struct {
	lifecycle *heldSessionLifecycle
	cleanup   *heldCleanupStack
	backend   *fixture.NATHoldBackend
	request   *heldNATRequest
	observed  fixture.NATEchoRecord
	profileID uint64
	protocol  bool
	closeOnce sync.Once
}

func newHeldNATSession(ctx context.Context, input heldNATInput) (*heldNATSession, error) {
	return newHeldNATSessionWithDependencies(ctx, input, activeHeldNATDependencies())
}

func newHeldNATSessionWithDependencies(ctx context.Context, input heldNATInput, dependencies heldNATDependencies) (*heldNATSession, error) {
	if err := validateHeldPATClient(input.PATClient); err != nil {
		return nil, err
	}
	if ctx == nil || input.Dashboard == nil || input.Agent == nil || input.Plan.Kind != StressSessionNAT || input.Plan.ID.String() == "" || input.Plan.Ordinal < 1 || input.Plan.Agent.Int() < 1 {
		return nil, ErrInvalidHeldNATSession
	}
	if err := validateHeldReadiness(input.Agent, input.Readiness); err != nil {
		return nil, err
	}
	stateClient := input.PATClient
	baseline, err := dependencies.snapshotState(ctx, stateClient)
	if err != nil {
		return nil, fmt.Errorf("snapshot NAT IOStream baseline: %w", err)
	}
	lifetimeContext := input.LifetimeContext
	if lifetimeContext == nil {
		lifetimeContext = ctx
	}
	lifecycle, err := newHeldSessionLifecycle(lifetimeContext, input.Plan, "", 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("create held NAT lifecycle: %w", err)
	}
	backend, err := fixture.StartNATHoldBackend()
	if err != nil {
		return nil, fmt.Errorf("start held NAT backend: %w", err)
	}
	session := &heldNATSession{lifecycle: lifecycle, cleanup: newHeldCleanupStack(), backend: backend}
	if err := session.cleanup.Push(heldCleanupAction{name: "close NAT backend", cleanup: func(context.Context) error { return dependencies.closeBackend(session.backend) }}); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	domain, err := heldNATDomain(input.Plan.ID.String())
	if err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	name := "agentcompat-held-nat-" + domain[:strings.Index(domain, ".")]
	profileID, err := dependencies.createProfile(ctx, input.Dashboard, backend, input.Readiness.ServerID, name, domain)
	if err != nil {
		return nil, rollbackHeldNAT(session, fmt.Errorf("create held NAT profile: %w", err))
	}
	session.profileID = profileID
	if err := session.cleanup.Push(heldCleanupAction{name: "NAT profile", cleanup: func(cleanupCtx context.Context) error {
		return dependencies.deleteProfile(cleanupCtx, input.Dashboard, session.profileID)
	}}); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	capability, err := dependencies.register(ctx, input.PATClient, heldIOStreamCapabilityIdentity{Purpose: client.IOStreamCapabilityPurposeNAT, ServerID: input.Readiness.ServerID, ResourceID: session.profileID})
	if err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	if err := session.cleanup.Push(heldCleanupAction{name: "unregister NAT capability", cleanup: func(cleanupCtx context.Context) error {
		return dependencies.unregisterCapability(cleanupCtx, capability)
	}}); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	if err := session.cleanup.Push(heldCleanupAction{name: "wait for NAT stream absence", cleanup: func(cleanupCtx context.Context) error {
		return dependencies.waitExpectation(cleanupCtx, capability, stateClient, baseline, true)
	}}); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	if err := session.cleanup.Push(heldCleanupAction{name: "cancel NAT capability", cleanup: func(cleanupCtx context.Context) error { return dependencies.cancelCapability(cleanupCtx, capability) }}); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	request, err := dependencies.startRequest(ctx, input.Dashboard.Endpoint(), domain, input.Plan.ID.String(), capability.HeaderCapability())
	if err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	session.request = request
	if err := session.cleanup.Push(heldCleanupAction{name: "held NAT request", cleanup: func(cleanupCtx context.Context) error {
		closeErr := dependencies.closeRequest(request)
		requestErr := <-request.result
		if errors.Is(requestErr, net.ErrClosed) {
			requestErr = nil
		}
		return errors.Join(closeErr, requestErr, cleanupCtx.Err())
	}}); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	if err := dependencies.waitRequestObserved(ctx, backend); err != nil {
		return nil, rollbackHeldNAT(session, fmt.Errorf("prove held NAT request: %w", err))
	}
	observed, err := dependencies.waitRequest(ctx, backend)
	if err != nil {
		return nil, rollbackHeldNAT(session, fmt.Errorf("read held NAT request: %w", err))
	}
	if err := dependencies.proveRequest(observed, domain, input.Plan.ID.String()); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	session.observed = observed
	session.protocol = true
	streamID, err := dependencies.waitCapability(ctx, capability)
	if err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	if err := dependencies.setStreamID(lifecycle, streamID); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	if err := dependencies.waitExpectation(ctx, capability, stateClient, baseline, false); err != nil {
		return nil, rollbackHeldNAT(session, fmt.Errorf("prove held NAT IOStream: %w", err))
	}
	if err := lifecycle.markLive(nil); err != nil {
		return nil, rollbackHeldNAT(session, err)
	}
	return session, nil
}

func rollbackHeldNAT(session *heldNATSession, original error) error {
	return errors.Join(original, session.rollback())
}

func (session *heldNATSession) rollback() error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(session.lifecycle.baseContext), 30*time.Second)
	defer cancel()
	return session.cleanup.Run(ctx)
}

func (session *heldNATSession) Plan() StressSessionPlan { return session.lifecycle.Plan() }
func (session *heldNATSession) WaitLive(ctx context.Context) error {
	return session.lifecycle.WaitLive(ctx)
}
func (session *heldNATSession) WaitClosed(ctx context.Context) error {
	return session.lifecycle.WaitClosed(ctx)
}

func (session *heldNATSession) Done() <-chan struct{}      { return session.lifecycle.Done() }
func (session *heldNATSession) CloseResult() error         { return session.lifecycle.CloseResult() }
func (session *heldNATSession) IOStreamID() (string, bool) { return session.lifecycle.IOStreamID() }
func (session *heldNATSession) ProtocolProved() bool       { return session.protocol }

func (session *heldNATSession) Close(ctx context.Context) error {
	owner, won := session.lifecycle.beginClose()
	if won {
		session.closeOnce.Do(func() {
			go func() {
				cleanupCtx, cancel := owner.cleanupContext()
				defer cancel()
				owner.markClosed(session.cleanup.Run(cleanupCtx))
			}()
		})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return session.lifecycle.WaitClosed(ctx)
}

func heldNATDomain(identity string) (string, error) {
	var builder strings.Builder
	for _, character := range strings.ToLower(identity) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			builder.WriteRune(character)
		}
	}
	if builder.Len() == 0 {
		return "", ErrInvalidHeldNATSession
	}
	return builder.String() + ".agentcompat-nat.invalid", nil
}

var _ heldSession = (*heldNATSession)(nil)
