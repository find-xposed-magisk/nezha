//go:build linux

package scenario

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

var terminalSizePattern = regexp.MustCompile(`compat-size=([0-9]+) ([0-9]+)`)

type terminalFrameConnection interface {
	WriteFrame(context.Context, client.Frame) error
	ReadFrame(context.Context) (client.Frame, error)
}

type terminalExitInput struct {
	InitialOutput []byte
	ExitSentAt    time.Time
	Now           func() time.Time
}

type terminalOutputReadInput struct {
	InitialOutput []byte
	ExitSentAt    time.Time
	Now           func() time.Time
}

type terminalOutputResult struct {
	Output         string
	MarkerObserved bool
	SizeObserved   bool
	StreamClosed   bool
	Rows           uint32
	Cols           uint32
	CloseCode      int
	CloseElapsed   time.Duration
}

func executeTerminalExit(ctx context.Context, input terminalExitInput, connection terminalFrameConnection) (terminalOutputResult, error) {
	contractContext, cancelContract := context.WithDeadline(ctx, input.ExitSentAt.Add(terminalShutdownContract+terminalShutdownHarnessMargin))
	defer cancelContract()
	if err := connection.WriteFrame(contractContext, client.Frame{Type: client.FrameText, Payload: []byte(terminalCommand)}); err != nil {
		return terminalOutputResult{}, err
	}
	return readTerminalOutput(contractContext, terminalOutputReadInput{InitialOutput: input.InitialOutput, ExitSentAt: input.ExitSentAt, Now: input.Now}, connection.ReadFrame)
}

func readTerminalOutput(ctx context.Context, input terminalOutputReadInput, read func(context.Context) (client.Frame, error)) (terminalOutputResult, error) {
	var output bytes.Buffer
	output.Write(input.InitialOutput)
	result := terminalOutputResult{Output: output.String()}
	observeTerminalOutput(&result, output.Bytes())
	for {
		frame, err := read(ctx)
		if err != nil {
			result.Output = output.String()
			result.CloseCode = closeErrorCode(err)
			var closeError *client.WebSocketCloseError
			if result.MarkerObserved && result.SizeObserved && errors.As(err, &closeError) && terminalCloseCodeAccepted(closeError.Code) {
				result.StreamClosed = true
				result.CloseElapsed = input.Now().Sub(input.ExitSentAt)
				return result, nil
			}
			return result, err
		}
		output.Write(frame.Payload)
		observeTerminalOutput(&result, output.Bytes())
	}
}

func observeTerminalOutput(result *terminalOutputResult, output []byte) {
	result.MarkerObserved = bytes.Contains(output, []byte(terminalMarker))
	matches := terminalSizePattern.FindSubmatch(output)
	if len(matches) != 3 {
		return
	}
	rows, rowsErr := strconv.ParseUint(string(matches[1]), 10, 32)
	cols, colsErr := strconv.ParseUint(string(matches[2]), 10, 32)
	if rowsErr == nil && colsErr == nil {
		result.SizeObserved = true
		result.Rows = uint32(rows)
		result.Cols = uint32(cols)
	}
}

func terminalCloseWithinContract(elapsed time.Duration) bool {
	return elapsed <= terminalShutdownContract+terminalShutdownHarnessMargin
}

func terminalCloseCodeAccepted(code int) bool {
	return code == 1000 || code == 1006
}

func closeErrorCode(err error) int {
	var closeError *client.WebSocketCloseError
	if errors.As(err, &closeError) {
		return closeError.Code
	}
	return 0
}

func terminalOutputDetails(output terminalOutputResult, err error) string {
	return fmt.Sprintf("marker=%t size_observed=%t rows=%d cols=%d closed=%t close_code=%d close_elapsed_ms=%d close_limit_ms=%d output=%q error=%s", output.MarkerObserved, output.SizeObserved, output.Rows, output.Cols, output.StreamClosed, output.CloseCode, output.CloseElapsed.Milliseconds(), (terminalShutdownContract + terminalShutdownHarnessMargin).Milliseconds(), output.Output, errorText(err))
}
