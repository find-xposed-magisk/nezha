//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

const (
	heldTerminalCleanupTimeout = 10 * time.Second
	heldTerminalPumpCapacity   = 32
	heldTerminalGracePeriod    = time.Second
)

var (
	ErrInvalidHeldTerminalInput = errors.New("held terminal input is invalid")
	ErrHeldTerminalProtocol     = errors.New("held terminal protocol proof failed")
	ErrInvalidHeldPATClient     = errors.New("held PAT client is invalid")
)

type heldTerminalInput struct {
	Dashboard       *dashboard.Dashboard
	PATClient       *client.Client
	Agent           *agent.Agent
	Readiness       agent.Readiness
	Plan            StressSessionPlan
	LifetimeContext context.Context
}

type heldTerminalSession struct {
	lifecycle  *heldSessionLifecycle
	stack      *heldCleanupStack
	connection heldTerminalConnection
	pump       heldTerminalPump
	protocol   bool
}

type heldTerminalConnection interface {
	WriteFrame(context.Context, client.Frame) error
	Close() error
}

type heldTerminalPump interface {
	Events() <-chan client.Frame
	Done() <-chan struct{}
	Err() error
	Stop(context.Context) error
	Wait(context.Context) error
}

func newHeldTerminalSession(ctx context.Context, input heldTerminalInput) (*heldTerminalSession, error) {
	if err := validateHeldTerminalInput(ctx, input); err != nil {
		return nil, err
	}
	if err := validateHeldPATClient(input.PATClient); err != nil {
		return nil, err
	}
	if err := validateHeldReadiness(input.Agent, input.Readiness); err != nil {
		return nil, err
	}
	stateClient := input.PATClient
	baseline, err := stateClient.IOStreamState(ctx)
	if err != nil {
		return nil, fmt.Errorf("snapshot terminal IOStream state: %w", err)
	}
	capability, err := registerHeldIOStreamCapability(ctx, input.PATClient, heldIOStreamCapabilityIdentity{Purpose: client.IOStreamCapabilityPurposeTerminal, ServerID: input.Readiness.ServerID})
	if err != nil {
		return nil, fmt.Errorf("register terminal capability: %w", err)
	}
	stack := newHeldCleanupStack()
	if err := stack.Push(heldCleanupAction{name: "unregister terminal capability", cleanup: capability.Unregister}); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	created, err := client.DoREST[terminalCreateRequest, terminalCreateResponse](ctx, input.PATClient, client.RESTRequest[terminalCreateRequest]{
		Method: http.MethodPost, Path: "/api/v1/terminal", Body: &terminalCreateRequest{Protocol: "grpc", ServerID: input.Readiness.ServerID},
		IOStreamCapability: capability.HeaderCapability(),
	})
	if err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, fmt.Errorf("create held terminal: %w", err))
	}
	streamID, err := capability.Wait(ctx)
	if err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if streamID != created.SessionID {
		return nil, rollbackHeldTerminal(ctx, stack, fmt.Errorf("terminal stream mismatch: response_and_capability_ids_differ: %w", ErrHeldTerminalProtocol))
	}
	if err := stack.Push(heldCleanupAction{name: "wait for terminal stream absence", cleanup: func(cleanupContext context.Context) error {
		return capability.waitExpectation(cleanupContext, stateClient, baseline, true)
	}}); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if err := stack.Push(heldCleanupAction{name: "cancel terminal capability", cleanup: capability.Cancel}); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if err := validateHeldTerminalResponse(created, input.Readiness.ServerID); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	lifetimeContext := input.LifetimeContext
	if lifetimeContext == nil {
		lifetimeContext = ctx
	}
	lifecycle, err := newHeldSessionLifecycle(lifetimeContext, input.Plan, streamID, heldTerminalCleanupTimeout)
	if err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	connection, err := input.PATClient.DialWebSocket(ctx, "/api/v1/ws/terminal/"+created.SessionID)
	if err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	resize, err := terminalResizeFrame(132, 43)
	if err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	pump, err := newHeldWebSocketPump(lifetimeContext, connection, heldTerminalPumpCapacity)
	if err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if err := stack.Push(heldCleanupAction{name: "close terminal WebSocket", cleanup: func(context.Context) error { return connection.Close() }}); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if err := stack.Push(heldCleanupAction{name: "stop terminal WebSocket pump", cleanup: func(cleanupContext context.Context) error {
		err := pump.Stop(cleanupContext)
		if isExpectedHeldTerminalClose(err) {
			return nil
		}
		return err
	}}); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if err := stack.Push(heldCleanupAction{name: "await terminal stream release", cleanup: func(cleanupContext context.Context) error {
		graceContext, cancelGrace := context.WithTimeout(cleanupContext, heldTerminalGracePeriod)
		defer cancelGrace()
		return pump.Wait(graceContext)
	}}); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if err := connection.WriteFrame(ctx, resize); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	command := heldTerminalCommand(input.Plan.ID.String())
	proof := newHeldTerminalProof(input.Plan.ID.String())
	if err := writeHeldTerminalCommandAfterFirstPumpFrame(ctx, connection, pump, proof, command); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	if err := stack.Push(heldCleanupAction{name: "release held terminal command", cleanup: func(cleanupContext context.Context) error {
		return connection.WriteFrame(cleanupContext, client.Frame{Type: client.FrameText, Payload: []byte("\n")})
	}}); err != nil {
		return nil, rollbackHeldTerminal(ctx, stack, err)
	}
	for {
		select {
		case frame, ok := <-pump.Events():
			if !ok {
				return nil, rollbackHeldTerminal(ctx, stack, errors.Join(heldTerminalProofDiagnostics(proof, pump.Err()), proof.Failure(), ErrHeldTerminalProtocol))
			}
			proof.Consume(frame)
			if proof.Complete() {
				if err := capability.waitExpectation(ctx, stateClient, baseline, false); err != nil {
					return nil, rollbackHeldTerminal(ctx, stack, err)
				}
				if markErr := lifecycle.markLive(nil); markErr != nil {
					return nil, rollbackHeldTerminal(ctx, stack, markErr)
				}
				return &heldTerminalSession{lifecycle: lifecycle, stack: stack, connection: connection, pump: pump, protocol: true}, nil
			}
		case <-ctx.Done():
			return nil, rollbackHeldTerminal(ctx, stack, errors.Join(heldTerminalProofDiagnostics(proof, pump.Err()), fmt.Errorf("terminal proof timeout: %w", ctx.Err())))
		}
	}
}

func isExpectedHeldTerminalClose(err error) bool {
	var closeErr *client.WebSocketCloseError
	return errors.As(err, &closeErr) && (closeErr.Code == 1000 || closeErr.Code == 1006)
}

func heldTerminalProofDiagnostics(proof *heldTerminalProof, pumpErr error) error {
	return fmt.Errorf("terminal proof ended: frames=%d bytes=%d first_frame=%s marker=%t rows=%d cols=%d pump_error=%v", proof.FrameCount(), proof.ByteCount(), proof.FirstFrameType(), hasHeldTerminalMarker(proof.buffer, proof.marker), proof.rows, proof.cols, pumpErr)
}

func writeHeldTerminalCommandAfterFirstPumpFrame(ctx context.Context, connection heldTerminalConnection, pump heldTerminalPump, proof *heldTerminalProof, command string) error {
	select {
	case frame, ok := <-pump.Events():
		if !ok {
			return errors.Join(pump.Err(), proof.Failure(), ErrHeldTerminalProtocol)
		}
		proof.Consume(frame)
	case <-ctx.Done():
		return ctx.Err()
	}
	return connection.WriteFrame(ctx, client.Frame{Type: client.FrameText, Payload: []byte(command)})
}

func validateHeldTerminalInput(ctx context.Context, input heldTerminalInput) error {
	if ctx == nil || input.Dashboard == nil || input.PATClient == nil || input.Agent == nil || input.Readiness.ServerID == 0 || input.Readiness.UUID == "" || input.Readiness.UUID != input.Agent.UUID() || input.Plan.Kind != StressSessionTerminal || input.Plan.ID.String() == "" || input.Plan.Ordinal < 1 || input.Plan.Agent.Int() < 1 {
		return ErrInvalidHeldTerminalInput
	}
	return nil
}

func validateHeldPATClient(clientInstance *client.Client) error {
	if clientInstance == nil {
		return ErrInvalidHeldPATClient
	}
	return nil
}

func validateHeldTerminalResponse(response terminalCreateResponse, serverID uint64) error {
	if response.SessionID == "" || response.ServerID != serverID {
		return fmt.Errorf("created terminal identity is incomplete_or_wrong_server: %w", ErrHeldTerminalProtocol)
	}
	return nil
}

func rollbackHeldTerminal(ctx context.Context, stack *heldCleanupStack, original error) error {
	rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), heldTerminalCleanupTimeout)
	defer cancel()
	return errors.Join(original, stack.Run(rollbackContext))
}

func (session *heldTerminalSession) Plan() StressSessionPlan { return session.lifecycle.Plan() }

func (session *heldTerminalSession) WaitLive(ctx context.Context) error {
	return session.lifecycle.WaitLive(ctx)
}

func (session *heldTerminalSession) IOStreamID() (string, bool) {
	return session.lifecycle.IOStreamID()
}

func (session *heldTerminalSession) ProtocolProved() bool { return session.protocol }

func (session *heldTerminalSession) WaitClosed(ctx context.Context) error {
	return session.lifecycle.WaitClosed(ctx)
}

func (session *heldTerminalSession) Done() <-chan struct{} { return session.lifecycle.Done() }
func (session *heldTerminalSession) CloseResult() error    { return session.lifecycle.CloseResult() }

func (session *heldTerminalSession) Close(ctx context.Context) error {
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

func newHeldTerminalSessionForTest(lifecycle *heldSessionLifecycle, stack *heldCleanupStack) *heldTerminalSession {
	return &heldTerminalSession{lifecycle: lifecycle, stack: stack}
}

var _ heldSession = (*heldTerminalSession)(nil)
