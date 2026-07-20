//go:build linux && agentcompat

package scenario

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

const fdDiagnosticShellTargetEnv = "NEZHA_AGENTCOMPAT_FD_DIAGNOSTIC_TARGET"

func TestFDDiagnosticCollector_TracksChildDescriptorLifecycle(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	target := filepath.Join(t.TempDir(), "fd-diagnostic-target")
	require.NoError(t, os.WriteFile(target, []byte("target"), 0o600))
	child := startFDDiagnosticShell(t, ctx, target)
	defer func() { require.NoError(t, child.close()) }()
	identity := agent.ProcessIdentity{Generation: 1, PID: child.command.Process.Pid}
	ordinal, err := NewStressAgentOrdinal(1)
	require.NoError(t, err)
	process, err := NewStressAgentProcess(ordinal, identity.PID)
	require.NoError(t, err)
	tail := fdDiagnosticChildTailControl{secondStarted: make(chan struct{}), releaseSecond: make(chan struct{})}
	collector := newFDDiagnosticCollector(fdDiagnosticCollectorSpec{Enabled: true, TailResultCapacity: 8, TailInterval: 0, Sample: tail.sampler()})
	collector.RecordBaseline(fdDiagnosticChildWindow(t, ctx, process, identity))
	require.NoError(t, child.send("exec 3<\"$"+fdDiagnosticShellTargetEnv+"\"; echo opened"))
	opened, err := child.nextLine(ctx)
	require.NoError(t, err)
	require.Equal(t, "opened", opened)
	end := fdDiagnosticChildWindow(t, ctx, process, identity)

	// When
	collector.RecordEnd(ctx, end)
	tail.waitSecondStart(t, ctx)
	require.NoError(t, child.send("exec 3<&-; echo closed"))
	closed, err := child.nextLine(ctx)
	require.NoError(t, err)
	require.Equal(t, "closed", closed)
	close(tail.releaseSecond)
	records := collector.WaitRecords()

	// Then
	require.Len(t, records, 1)
	record := records[0]
	added := fdDiagnosticObservationTarget(record.AddedFinal, target)
	require.NotNil(t, added)
	require.Equal(t, "cleared_during_tail", fdDiagnosticLifecycleStatus(record.Lifecycle, *added))
	require.NotNil(t, fdDiagnosticObservationTarget(record.Tail[0].FDObservations, target))
	for _, sample := range record.Tail[1:] {
		require.Nil(t, fdDiagnosticObservationTarget(sample.FDObservations, target))
	}
	for _, window := range []*fdDiagnosticWindow{record.Baseline, record.End} {
		for _, sample := range window.Samples {
			require.False(t, sample.ObservedAt.IsZero())
		}
	}
	for _, sample := range record.Tail {
		require.False(t, sample.ObservedAt.IsZero())
	}
}

type fdDiagnosticShell struct {
	command  *exec.Cmd
	input    io.WriteCloser
	cancel   context.CancelFunc
	lines    <-chan fdDiagnosticShellLine
	waitOnce sync.Once
	waitErr  error
}

type fdDiagnosticShellLine struct {
	text string
	err  error
}

func startFDDiagnosticShell(t *testing.T, ctx context.Context, target string) *fdDiagnosticShell {
	t.Helper()
	processCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, "/bin/sh")
	command.Env = append(os.Environ(), fdDiagnosticShellTargetEnv+"="+target)
	input, err := command.StdinPipe()
	require.NoError(t, err)
	output, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	lines := make(chan fdDiagnosticShellLine)
	go collectFDDiagnosticShellLines(processCtx, output, lines)
	return &fdDiagnosticShell{command: command, input: input, cancel: cancel, lines: lines}
}

func collectFDDiagnosticShellLines(ctx context.Context, output io.Reader, lines chan<- fdDiagnosticShellLine) {
	defer close(lines)
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		select {
		case lines <- fdDiagnosticShellLine{text: scanner.Text()}:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case lines <- fdDiagnosticShellLine{err: err}:
		case <-ctx.Done():
		}
	}
}

func (child *fdDiagnosticShell) close() error {
	sendErr := child.send("exit")
	var killErr error
	if sendErr != nil {
		killErr = child.command.Process.Kill()
	}
	waitErr := child.wait()
	if sendErr == nil {
		return waitErr
	}
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	if killErr == nil {
		return sendErr
	}
	return errors.Join(sendErr, killErr, waitErr)
}

func (child *fdDiagnosticShell) wait() error {
	child.waitOnce.Do(func() {
		child.waitErr = child.command.Wait()
		child.cancel()
	})
	return child.waitErr
}

func (child *fdDiagnosticShell) send(command string) error {
	_, err := fmt.Fprintln(child.input, command)
	return err
}

func (child *fdDiagnosticShell) nextLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case line, open := <-child.lines:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !open {
			return "", io.EOF
		}
		return line.text, line.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type fdDiagnosticChildTailControl struct {
	secondStarted chan struct{}
	releaseSecond chan struct{}
}

func (control fdDiagnosticChildTailControl) sampler() fdDiagnosticSampleFunc {
	calls := 0
	return func(ctx context.Context, pid int) (processharness.Sample, error) {
		calls++
		if calls == 2 {
			close(control.secondStarted)
			select {
			case <-control.releaseSecond:
			case <-ctx.Done():
				return processharness.Sample{}, ctx.Err()
			}
		}
		return processharness.SampleProcessWithFDObservations(pid)
	}
}

func (control fdDiagnosticChildTailControl) waitSecondStart(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-control.secondStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func fdDiagnosticChildWindow(t *testing.T, ctx context.Context, process StressProcessIdentity, identity agent.ProcessIdentity) fdDiagnosticAgentWindow {
	t.Helper()
	window, err := processharness.SampleWindow(ctx, processharness.WindowSpec{PID: identity.PID, Interval: time.Nanosecond, CaptureFDObservations: true})
	require.NoError(t, err)
	return fdDiagnosticAgentWindow{Process: process, Identity: identity, Window: window}
}

func fdDiagnosticObservationTarget(observations []processharness.FDObservation, target string) *processharness.FDObservation {
	for _, observation := range observations {
		if observation.Target == target {
			return &observation
		}
	}
	return nil
}

func fdDiagnosticLifecycleStatus(lifecycle []fdDiagnosticLifecycle, observation processharness.FDObservation) string {
	for _, entry := range lifecycle {
		if entry.Observation == observation {
			return entry.Status
		}
	}
	return ""
}
