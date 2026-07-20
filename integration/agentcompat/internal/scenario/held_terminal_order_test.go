//go:build linux

package scenario

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

type heldTerminalOrderConnection struct {
	writes chan client.Frame
	gate   <-chan struct{}
}

func (connection *heldTerminalOrderConnection) WriteFrame(_ context.Context, frame client.Frame) error {
	if frame.Type == client.FrameText {
		<-connection.gate
	}
	connection.writes <- frame
	return nil
}

func (connection *heldTerminalOrderConnection) Close() error { return nil }

type heldTerminalOrderPump struct {
	events chan client.Frame
}

func (pump *heldTerminalOrderPump) Events() <-chan client.Frame { return pump.events }
func (pump *heldTerminalOrderPump) Done() <-chan struct{}       { return nil }
func (pump *heldTerminalOrderPump) Err() error                  { return nil }
func (pump *heldTerminalOrderPump) Stop(context.Context) error  { return nil }
func (pump *heldTerminalOrderPump) Wait(context.Context) error  { return nil }

func TestHeldTerminalCommandWaitsForFirstPumpFrame(t *testing.T) {
	// Given
	firstFrame := make(chan client.Frame)
	commandGate := make(chan struct{})
	connection := &heldTerminalOrderConnection{writes: make(chan client.Frame, 1), gate: commandGate}
	pump := &heldTerminalOrderPump{events: firstFrame}
	proof := newHeldTerminalProof("marker")

	// When
	commandWritten := make(chan error, 1)
	go func() {
		commandWritten <- writeHeldTerminalCommandAfterFirstPumpFrame(context.Background(), connection, pump, proof, heldTerminalCommand("marker"))
	}()
	firstFrame <- client.Frame{Type: client.FrameText, Payload: []byte("first PTY output")}
	select {
	case err := <-commandWritten:
		t.Fatalf("command completed before the first frame barrier was released: %v", err)
	default:
	}
	close(commandGate)
	command := <-connection.writes

	// Then
	require.Equal(t, client.FrameText, command.Type)
	require.NoError(t, <-commandWritten)
}

func TestTerminalWireFormatsRemainAgentCompatible(t *testing.T) {
	// Given
	resize := mustTerminalResizeFrame(132, 43)
	command := heldTerminalCommand("marker")

	// When
	// Then
	require.Equal(t, client.FrameBinary, resize.Type)
	require.Equal(t, byte(1), resize.Payload[0])
	require.JSONEq(t, `{"Cols":132,"Rows":43}`, string(resize.Payload[1:]))
	require.Equal(t, client.FrameText, client.Frame{Type: client.FrameText, Payload: []byte(command)}.Type)
	require.NotEqual(t, byte(0), []byte(command)[0])
}
