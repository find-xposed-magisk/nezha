//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

const (
	heldLegacyFMCleanupTimeout = 10 * time.Second
	heldLegacyFMPumpCapacity   = 8
)

var (
	ErrInvalidHeldLegacyFMInput = errors.New("held legacy FM input is invalid")
	ErrHeldLegacyFMProtocol     = errors.New("held legacy FM protocol proof failed")
	heldLegacyFMRootName        = regexp.MustCompile(`[^a-z0-9]+`)
)

type heldLegacyFMInput struct {
	Dashboard       *dashboard.Dashboard
	PATClient       *client.Client
	Agent           *agent.Agent
	Readiness       agent.Readiness
	Plan            StressSessionPlan
	LifetimeContext context.Context
}

type heldLegacyFMSession struct {
	lifecycle  *heldSessionLifecycle
	stack      *heldCleanupStack
	connection heldLegacyFMConnection
	pump       heldLegacyFMPump
	protocol   bool
}

func newHeldLegacyFMSession(ctx context.Context, input heldLegacyFMInput) (*heldLegacyFMSession, error) {
	return newHeldLegacyFMSessionWithDependencies(ctx, input, defaultHeldLegacyFMDependencies())
}

func newHeldLegacyFMSessionWithDependencies(ctx context.Context, input heldLegacyFMInput, dependencies heldLegacyFMDependencies) (*heldLegacyFMSession, error) {
	if err := validateHeldLegacyFMInput(ctx, input); err != nil {
		return nil, err
	}
	if err := validateHeldPATClient(input.PATClient); err != nil {
		return nil, err
	}
	if err := validateHeldReadiness(input.Agent, input.Readiness); err != nil {
		return nil, err
	}
	stateClient := input.PATClient
	baseline, err := dependencies.SnapshotState(ctx, stateClient)
	if err != nil {
		return nil, fmt.Errorf("snapshot FM IOStream state: %w", err)
	}
	rootName := heldLegacyFMRootName.ReplaceAllString(strings.ToLower(input.Plan.ID.String()), "-")
	rootName = strings.Trim(rootName, "-")
	if rootName == "" {
		return nil, ErrInvalidHeldLegacyFMInput
	}
	root, err := fixture.NewAgentRoot(input.Agent.WorkspaceRoot(), "held-fm-"+rootName)
	if err != nil {
		return nil, fmt.Errorf("create held FM fixture root: %w", err)
	}
	stack := newHeldCleanupStack()
	if err := stack.Push(heldCleanupAction{name: "remove FM fixture root", cleanup: func(cleanupContext context.Context) error {
		return dependencies.RemoveFixture(cleanupContext, input.Agent.WorkspaceRoot(), root.Absolute())
	}}); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	listDirectory, err := root.Path("list")
	if err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := os.Mkdir(listDirectory.String(), 0o700); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := os.WriteFile(filepath.Join(listDirectory.String(), "entry.txt"), []byte("entry"), 0o600); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	capability, err := dependencies.Register(ctx, input.PATClient, heldIOStreamCapabilityIdentity{Purpose: client.IOStreamCapabilityPurposeFileManager, ServerID: input.Readiness.ServerID})
	if err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := pushHeldLegacyFMCapabilityCleanup(stack, heldLegacyFMCapabilityCleanup{
		Unregister: capability.Unregister,
		Absence: func(cleanupContext context.Context) error {
			return dependencies.WaitForState(cleanupContext, stateClient, baseline, capability, true)
		},
		Cancel: capability.Cancel,
	}); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	sessionID, err := dependencies.CreateSession(ctx, input.PATClient, input.Readiness.ServerID, capability.HeaderCapability())
	if err != nil {
		_, waitErr := capability.Wait(ctx)
		return nil, rollbackHeldLegacyFM(ctx, stack, errors.Join(err, waitErr))
	}
	streamID, waitErr := capability.Wait(ctx)
	if waitErr != nil || streamID != sessionID {
		mismatchErr := error(nil)
		if waitErr == nil {
			mismatchErr = heldLegacyFMStreamMismatchError()
		}
		return nil, rollbackHeldLegacyFM(ctx, stack, errors.Join(waitErr, mismatchErr))
	}
	lifetimeContext := input.LifetimeContext
	if lifetimeContext == nil {
		lifetimeContext = ctx
	}
	lifecycle, err := newHeldSessionLifecycle(lifetimeContext, input.Plan, sessionID, heldLegacyFMCleanupTimeout)
	if err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	connection, err := dependencies.DialWebSocket(ctx, input.PATClient, "/api/v1/ws/file/"+sessionID)
	if err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := stack.Push(heldCleanupAction{name: "close FM WebSocket", cleanup: func(context.Context) error { return connection.Close() }}); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	pump, err := dependencies.NewPump(lifetimeContext, connection, heldLegacyFMPumpCapacity)
	if err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := stack.Push(heldCleanupAction{name: "stop FM WebSocket pump", cleanup: pump.Stop}); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	dispatcher := legacyFMCommandDispatcher{writer: connection, root: root}
	if err := dispatcher.list(ctx, "list"); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := proveHeldLegacyFMList(ctx, pump, listDirectory.String()); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := dependencies.WaitForState(ctx, stateClient, baseline, capability, false); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	if err := lifecycle.markLive(nil); err != nil {
		return nil, rollbackHeldLegacyFM(ctx, stack, err)
	}
	return &heldLegacyFMSession{lifecycle: lifecycle, stack: stack, connection: connection, pump: pump, protocol: true}, nil
}

func heldLegacyFMStreamMismatchError() error {
	return fmt.Errorf("FM stream identity mismatch: %w", ErrHeldLegacyFMProtocol)
}

func validateHeldLegacyFMInput(ctx context.Context, input heldLegacyFMInput) error {
	if ctx == nil || input.Dashboard == nil || input.Agent == nil || input.Plan.Kind != StressSessionFM || input.Plan.ID.String() == "" || input.Plan.Ordinal < 1 || input.Plan.Agent.Int() < 1 {
		return ErrInvalidHeldLegacyFMInput
	}
	if err := validateHeldPATClient(input.PATClient); err != nil {
		return err
	}
	return nil
}

func proveHeldLegacyFMList(ctx context.Context, pump heldLegacyFMPump, wantPath string) error {
	select {
	case frame, ok := <-pump.Events():
		if !ok {
			return errors.Join(pump.Err(), ErrHeldLegacyFMProtocol)
		}
		if frame.Type != client.FrameBinary {
			return fmt.Errorf("FM list response frame type=%s: %w", frame.Type, ErrHeldLegacyFMProtocol)
		}
		parsed, err := parseLegacyFMList(frame.Payload)
		if err != nil {
			return err
		}
		if parsed.Path != wantPath || len(parsed.Entries) != 1 || parsed.Entries[0].Name != "entry.txt" || parsed.Entries[0].Dir {
			return ErrHeldLegacyFMProtocol
		}
		return nil
	case <-pump.Done():
		return errors.Join(pump.Err(), ErrHeldLegacyFMProtocol)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func rollbackHeldLegacyFM(ctx context.Context, stack *heldCleanupStack, original error) error {
	rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), heldLegacyFMCleanupTimeout)
	defer cancel()
	return errors.Join(original, stack.Run(rollbackContext))
}

func (session *heldLegacyFMSession) Plan() StressSessionPlan { return session.lifecycle.Plan() }
func (session *heldLegacyFMSession) WaitLive(ctx context.Context) error {
	return session.lifecycle.WaitLive(ctx)
}
func (session *heldLegacyFMSession) IOStreamID() (string, bool) {
	return session.lifecycle.IOStreamID()
}
func (session *heldLegacyFMSession) ProtocolProved() bool { return session.protocol }
func (session *heldLegacyFMSession) WaitClosed(ctx context.Context) error {
	return session.lifecycle.WaitClosed(ctx)
}

func (session *heldLegacyFMSession) Done() <-chan struct{} { return session.lifecycle.Done() }
func (session *heldLegacyFMSession) CloseResult() error    { return session.lifecycle.CloseResult() }

func (session *heldLegacyFMSession) Close(ctx context.Context) error {
	owner, won := session.lifecycle.beginClose()
	if !won {
		return session.lifecycle.WaitClosed(ctx)
	}
	go func() {
		cleanupContext, cancel := owner.cleanupContext()
		cleanupErr := session.stack.Run(cleanupContext)
		if cleanupContext.Err() != nil {
			cleanupErr = errors.Join(cleanupErr, cleanupContext.Err())
		}
		cancel()
		owner.markClosed(cleanupErr)
	}()
	return session.lifecycle.WaitClosed(ctx)
}

var _ heldSession = (*heldLegacyFMSession)(nil)
