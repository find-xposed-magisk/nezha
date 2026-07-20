//go:build linux && agentcompat

package scenario

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

type terminalDeadlineProbe struct {
	writeDeadline time.Time
}

func (probe *terminalDeadlineProbe) WriteFrame(ctx context.Context, _ client.Frame) error {
	probe.writeDeadline, _ = ctx.Deadline()
	return context.DeadlineExceeded
}

func (*terminalDeadlineProbe) ReadFrame(context.Context) (client.Frame, error) {
	return client.Frame{}, errors.New("read must not run after blocked write")
}

func TestTerminalOutputReader_ReturnsMarkerAndCloseEvidence(t *testing.T) {
	frames := make(chan client.Frame, 1)
	frames <- client.Frame{Type: client.FrameBinary, Payload: []byte("shell prompt\r\ncompat-size=43 132\r\ncompat-terminal\r\n")}
	close(frames)

	result, err := readTerminalOutput(context.Background(), terminalOutputReadInput{ExitSentAt: time.Unix(0, 0), Now: func() time.Time { return time.Unix(0, int64(time.Second)) }}, func(context.Context) (client.Frame, error) {
		frame, ok := <-frames
		if !ok {
			return client.Frame{}, &client.WebSocketCloseError{Code: 1000, Text: "normal closure"}
		}
		return frame, nil
	})

	require.NoError(t, err)
	require.True(t, result.MarkerObserved)
	require.True(t, result.StreamClosed)
	require.Equal(t, 1000, result.CloseCode)
	require.Equal(t, time.Second, result.CloseElapsed)
	require.Contains(t, result.Output, "compat-terminal")
}

func TestTerminalOutputReader_ObservesRequestedPTYSize(t *testing.T) {
	frames := make(chan client.Frame, 1)
	frames <- client.Frame{Type: client.FrameBinary, Payload: []byte("compat-size=43 132\r\ncompat-terminal\r\n")}
	close(frames)

	result, err := readTerminalOutput(context.Background(), terminalOutputReadInput{ExitSentAt: time.Unix(0, 0), Now: func() time.Time { return time.Unix(0, int64(time.Second)) }}, func(context.Context) (client.Frame, error) {
		frame, ok := <-frames
		if !ok {
			return client.Frame{}, &client.WebSocketCloseError{Code: 1000, Text: "normal closure"}
		}
		return frame, nil
	})

	require.NoError(t, err)
	require.True(t, result.SizeObserved)
	require.Equal(t, uint32(43), result.Rows)
	require.Equal(t, uint32(132), result.Cols)
}

func TestTerminalCommand_ReportsSizeBeforeMarkerAndExit(t *testing.T) {
	require.Equal(t, "printf 'compat-size='; stty size; printf 'compat-terminal\\n'; exit\n", terminalCommand)
}

func TestTerminalCloseContract_AllowsAgentTimeoutPlusHarnessMargin(t *testing.T) {
	require.True(t, terminalCloseWithinContract(terminalShutdownContract+terminalShutdownHarnessMargin))
	require.False(t, terminalCloseWithinContract(terminalShutdownContract+terminalShutdownHarnessMargin+time.Nanosecond))
}

func TestTerminalExit_UsesOneAbsoluteDeadlineForCommandWriteAndCloseRead(t *testing.T) {
	exitSentAt := time.Unix(100, 0)
	probe := &terminalDeadlineProbe{}

	_, err := executeTerminalExit(context.Background(), terminalExitInput{ExitSentAt: exitSentAt, Now: func() time.Time { return exitSentAt }}, probe)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, exitSentAt.Add(terminalShutdownContract+terminalShutdownHarnessMargin), probe.writeDeadline)
}

func TestForeignTerminalPATScopes_UseMinimumAttachScope(t *testing.T) {
	require.Equal(t, "nezha:server:exec", terminalAttachPATScope)
}

func TestTerminalOutputReader_RejectsNonCloseErrorAfterMarker(t *testing.T) {
	reads := 0

	result, err := readTerminalOutput(context.Background(), terminalOutputReadInput{ExitSentAt: time.Unix(0, 0), Now: func() time.Time { return time.Unix(0, int64(time.Second)) }}, func(context.Context) (client.Frame, error) {
		reads++
		if reads == 1 {
			return client.Frame{Type: client.FrameBinary, Payload: []byte("compat-size=43 132\r\ncompat-terminal\r\n")}, nil
		}
		return client.Frame{}, context.DeadlineExceeded
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, result.MarkerObserved)
	require.False(t, result.StreamClosed)
}

func TestTerminalOutputReader_RejectsProtocolCloseAfterMarker(t *testing.T) {
	reads := 0

	result, err := readTerminalOutput(context.Background(), terminalOutputReadInput{ExitSentAt: time.Unix(0, 0), Now: func() time.Time { return time.Unix(0, int64(time.Second)) }}, func(context.Context) (client.Frame, error) {
		reads++
		if reads == 1 {
			return client.Frame{Type: client.FrameBinary, Payload: []byte("compat-size=43 132\r\ncompat-terminal\r\n")}, nil
		}
		return client.Frame{}, &client.WebSocketCloseError{Code: 1002, Text: "protocol error"}
	})

	var closeError *client.WebSocketCloseError
	require.ErrorAs(t, err, &closeError)
	require.Equal(t, 1002, result.CloseCode)
	require.True(t, result.MarkerObserved)
	require.False(t, result.StreamClosed)
}

func TestTerminalResizeFrame_UsesAgentWireContract(t *testing.T) {
	frame, err := terminalResizeFrame(132, 43)

	require.NoError(t, err)
	require.Equal(t, client.FrameBinary, frame.Type)
	require.Equal(t, byte(1), frame.Payload[0])
	require.JSONEq(t, `{"Cols":132,"Rows":43}`, string(frame.Payload[1:]))
}
