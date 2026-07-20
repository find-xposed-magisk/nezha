//go:build linux

package scenario

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func TestHeldTerminalCommandSubmitsWithLineFeed(t *testing.T) {
	command := heldTerminalCommand("marker-session")

	require.Equal(t, byte('\n'), command[len(command)-1])
	require.NotContains(t, command, "exit\\n")
}

func TestHeldTerminalProofRejectsEchoOnlyExactTokens(t *testing.T) {
	proof := newHeldTerminalProof("marker-session")

	proof.Consume(client.Frame{Type: client.FrameText, Payload: []byte("printf 'compat-size='; stty size; printf 'marker-session\\n'; read -r held_terminal_release; exit\\n\r\ncompat-size=43 132\r\nmarker-session\r\n")})

	require.False(t, proof.Complete())
}

func TestHeldTerminalProofAcceptsMarkerAndExactSizeAcrossFrames(t *testing.T) {
	// Given
	proof := newHeldTerminalProof("marker-session")

	// When
	proof.Consume(client.Frame{Type: client.FrameText, Payload: []byte("prefix\r\n\x1emarker-session|43 ")})
	proof.Consume(client.Frame{Type: client.FrameBinary, Payload: []byte("132\x1f\r\n")})

	// Then
	require.True(t, proof.Complete())
	require.Equal(t, uint32(43), proof.Rows())
	require.Equal(t, uint32(132), proof.Columns())
}

func TestHeldTerminalProofIgnoresMarkerInEchoedCommand(t *testing.T) {
	// Given
	proof := newHeldTerminalProof("marker-session")

	// When
	proof.Consume(client.Frame{Type: client.FrameText, Payload: []byte("printf '\\036%s%s|' 'marker-' 'session'; stty size; printf '\\037'\r\n")})
	proof.Consume(client.Frame{Type: client.FrameText, Payload: []byte("\x1emarker-session|43 132\x1f\r\n")})

	// Then
	require.True(t, proof.Complete())
	require.Equal(t, uint32(43), proof.Rows())
	require.Equal(t, uint32(132), proof.Columns())
}

func TestHeldTerminalProofRejectsWrongMarkerAndWrongSize(t *testing.T) {
	// Given
	wrongMarker := newHeldTerminalProof("marker-session")
	wrongSize := newHeldTerminalProof("marker-session")

	// When
	wrongMarker.Consume(client.Frame{Type: client.FrameText, Payload: []byte("\x1emarker-other|43 132\x1f\r\n")})
	wrongSize.Consume(client.Frame{Type: client.FrameText, Payload: []byte("\x1emarker-session|42 132\x1f\r\n")})

	// Then
	require.False(t, wrongMarker.Complete())
	require.False(t, wrongSize.Complete())
}

func TestHeldTerminalProofAcceptsValidRecordAfterWrongMarkerInSameFrame(t *testing.T) {
	// Given
	proof := newHeldTerminalProof("marker-session")
	frame := []byte("\x1emarker-other|43 132\x1f\x1emarker-session|43 132\x1f")

	// When
	proof.Consume(client.Frame{Type: client.FrameText, Payload: frame})

	// Then
	require.True(t, proof.Complete())
}

func TestHeldTerminalProofAcceptsValidRecordAfterMalformedRecordInSameFrame(t *testing.T) {
	// Given
	proof := newHeldTerminalProof("marker-session")
	frame := []byte("\x1emarker-session|43 nope\x1f\x1emarker-session|43 132\x1f")

	// When
	proof.Consume(client.Frame{Type: client.FrameBinary, Payload: frame})

	// Then
	require.True(t, proof.Complete())
}

func TestHeldTerminalProofScansMultipleInvalidRecordsBeforeValidRecord(t *testing.T) {
	// Given
	proof := newHeldTerminalProof("marker-session")
	frame := []byte("noise\x1emarker-other|43 132\x1f\x1emarker-session|43 nope\x1f\x1emarker-session|43 132\x1f")

	// When
	proof.Consume(client.Frame{Type: client.FrameText, Payload: frame})

	// Then
	require.True(t, proof.Complete())
}

func TestHeldTerminalProofAcceptsFramedRecordAcrossFrames(t *testing.T) {
	proof := newHeldTerminalProof("marker-session")

	proof.Consume(client.Frame{Type: client.FrameBinary, Payload: []byte("prefix\x1emarker-session|43 ")})
	proof.Consume(client.Frame{Type: client.FrameBinary, Payload: []byte("132\x1f\r\n")})

	require.True(t, proof.Complete())
}

func TestHeldTerminalProofRejectsMalformedOrUnclosedRecord(t *testing.T) {
	malformed := newHeldTerminalProof("marker-session")
	unclosed := newHeldTerminalProof("marker-session")

	malformed.Consume(client.Frame{Type: client.FrameBinary, Payload: []byte("\x1emarker-session|43 nope\x1f")})
	unclosed.Consume(client.Frame{Type: client.FrameBinary, Payload: []byte("\x1emarker-session|43 132")})

	require.False(t, malformed.Complete())
	require.False(t, unclosed.Complete())
	require.Equal(t, []byte("\x1emarker-session|43 132"), unclosed.buffer)
}

func TestHeldTerminalProofAcceptsEchoThenFramedRecord(t *testing.T) {
	proof := newHeldTerminalProof("marker-session")

	proof.Consume(client.Frame{Type: client.FrameText, Payload: []byte("printf '\\036%s%s|' 'marker-' 'session'; stty size; printf '\\037' exit\\n\r\n")})
	require.False(t, proof.Complete())
	proof.Consume(client.Frame{Type: client.FrameBinary, Payload: []byte("\x1emarker-session|43 132\x1f")})

	require.True(t, proof.Complete())
}

func TestHeldTerminalProofRejectsClosedPumpBeforeProof(t *testing.T) {
	// Given
	proof := newHeldTerminalProof("marker-session")

	// When
	err := proof.Closed(errors.New("pump closed"))

	// Then
	require.Error(t, err)
	require.ErrorContains(t, err, "pump closed")
}

func TestHeldTerminalProofBoundsAccumulator(t *testing.T) {
	// Given
	proof := newHeldTerminalProof("marker-session")

	// When
	proof.Consume(client.Frame{Type: client.FrameText, Payload: make([]byte, heldTerminalProofLimit+1)})

	// Then
	require.LessOrEqual(t, len(proof.buffer), heldTerminalProofLimit)
}

func TestHeldTerminalResponseRequiresExactSessionServerIdentity(t *testing.T) {
	// Given
	response := terminalCreateResponse{SessionID: "session", ServerID: 9}

	// When
	err := validateHeldTerminalResponse(response, 8)

	// Then
	require.ErrorIs(t, err, ErrHeldTerminalProtocol)
	require.NoError(t, validateHeldTerminalResponse(response, 9))
}

func TestHeldTerminalInputRejectsMissingResourcesAndMismatchedReadiness(t *testing.T) {
	// Given
	plan := heldTestPlan(t)
	input := heldTerminalInput{Plan: plan, Readiness: agent.Readiness{ServerID: 7, UUID: "agent"}}

	// When
	err := validateHeldTerminalInput(context.Background(), input)

	// Then
	require.ErrorIs(t, err, ErrInvalidHeldTerminalInput)
}

func TestHeldTerminalInputRejectsMissingPATClientBeforeRemoteMutation(t *testing.T) {
	err := validateHeldPATClient(nil)

	require.ErrorIs(t, err, ErrInvalidHeldPATClient)
}

func TestHeldTerminalCommandKeepsShellHeldUntilInput(t *testing.T) {
	// Given
	command := heldTerminalCommand("marker-session")

	// Then
	require.Contains(t, command, "marker-")
	require.Contains(t, command, "session")
	require.Contains(t, command, "stty size")
	require.Contains(t, command, "read -r")
	require.Contains(t, command, "exit")
	require.Contains(t, command, "\\036")
	require.Contains(t, command, "\\037")
	require.NotEqual(t, "\n", command)
}

func TestHeldTerminalCleanupOwnerDoesNotUseCanceledWaiter(t *testing.T) {
	// Given
	cleanupStarted := make(chan struct{})
	cleanupRelease := make(chan struct{})
	lifecycle := heldTestLifecycle(t, "held-terminal-session")
	stack := newHeldCleanupStack()
	require.NoError(t, stack.Push(heldCleanupAction{name: "blocked", cleanup: func(cleanupContext context.Context) error {
		require.NoError(t, cleanupContext.Err())
		close(cleanupStarted)
		<-cleanupRelease
		return nil
	}}))
	session := newHeldTerminalSessionForTest(lifecycle, stack)

	// When
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	first := make(chan error, 1)
	go func() { first <- session.Close(canceled) }()
	<-cleanupStarted

	// Then
	require.ErrorIs(t, <-first, context.Canceled)
	close(cleanupRelease)
	require.NoError(t, session.Close(context.Background()))
}

func TestHeldTerminalCleanupStackReleasesHoldBeforeTransport(t *testing.T) {
	// Given
	stack := newHeldCleanupStack()
	var order []string
	require.NoError(t, stack.Push(heldCleanupAction{name: "absence", cleanup: func(context.Context) error {
		order = append(order, "absence")
		return nil
	}}))
	require.NoError(t, stack.Push(heldCleanupAction{name: "transport", cleanup: func(context.Context) error {
		order = append(order, "transport")
		return nil
	}}))
	require.NoError(t, stack.Push(heldCleanupAction{name: "release", cleanup: func(context.Context) error {
		order = append(order, "release")
		return nil
	}}))

	// When
	require.NoError(t, stack.Run(context.Background()))

	// Then
	require.Equal(t, []string{"release", "transport", "absence"}, order)
}
