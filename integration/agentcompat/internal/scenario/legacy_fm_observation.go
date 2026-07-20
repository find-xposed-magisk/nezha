//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type legacyFMResidueProbe struct {
	assertions    *AssertionSet
	agentPID      int
	session       string
	root          fixture.AgentRoot
	baseline      processharness.Sample
	sessionClient *client.Client
	producer      legacyFMProducerObservation
}

type legacyFMCountingWriter struct {
	frameCount int
}

func (writer *legacyFMCountingWriter) WriteFrame(context.Context, client.Frame) error {
	writer.frameCount++
	return nil
}

func probeLegacyFMRejectedPathDispatches(ctx context.Context, root fixture.AgentRoot) (int, error) {
	symlinkName := "rejected-symlink-parent"
	symlinkPath := filepath.Join(root.Absolute(), symlinkName)
	if err := os.Symlink(filepath.Dir(root.Absolute()), symlinkPath); err != nil {
		return 0, fmt.Errorf("create rejected-path symlink: %w", err)
	}
	defer os.Remove(symlinkPath)

	candidates := []string{
		filepath.Join(filepath.Dir(root.Absolute()), "outside-fm-root"),
		"../outside-fm-root",
		".",
		`C:\outside-fm-root`,
		`inside\outside`,
		symlinkName + "/file",
	}
	writer := &legacyFMCountingWriter{}
	dispatcher := legacyFMCommandDispatcher{writer: writer, root: root}
	for _, candidate := range candidates {
		operations := []func() error{
			func() error { return dispatcher.list(ctx, candidate) },
			func() error { return dispatcher.upload(ctx, candidate, 1) },
			func() error { return dispatcher.download(ctx, candidate) },
		}
		for _, operation := range operations {
			var pathErr *fixture.AgentPathError
			if err := operation(); !errors.As(err, &pathErr) {
				return writer.frameCount, fmt.Errorf("rejected FM path %q crossed dispatch boundary: %w", candidate, err)
			}
		}
	}
	return writer.frameCount, nil
}

func countLegacyFMFixtureOpenFiles(pid int, root fixture.AgentRoot) (int, error) {
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return 0, fmt.Errorf("read Agent file descriptors: %w", err)
	}
	count := 0
	rootPath := filepath.Clean(root.Absolute())
	for _, entry := range entries {
		target, readErr := os.Readlink(filepath.Join("/proc", fmt.Sprint(pid), "fd", entry.Name()))
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return 0, fmt.Errorf("read Agent file descriptor %s: %w", entry.Name(), readErr)
		}
		target = strings.TrimSuffix(target, " (deleted)")
		if target == rootPath || strings.HasPrefix(target, rootPath+string(filepath.Separator)) {
			count++
		}
	}
	return count, nil
}

func waitForLegacyFMFixtureOpenFilesClosed(ctx context.Context, pid int, root fixture.AgentRoot) (int, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		count, err := countLegacyFMFixtureOpenFiles(pid, root)
		if err != nil || count == 0 {
			return count, err
		}
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (probe legacyFMResidueProbe) run(ctx context.Context) error {
	sessionResidueCount, cleanupErr := waitForLegacyFMSessionCleanup(ctx, probe.sessionClient, probe.session)
	probe.assertions.Record("closed FM WebSocket removes session", cleanupErr == nil && sessionResidueCount == 0, fmt.Sprintf("fm_session_residue_count: %d; error=%s", sessionResidueCount, errorText(cleanupErr)))
	if cleanupErr != nil {
		return cleanupErr
	}

	producerErr := probe.producer.validate()
	probe.assertions.Record("FM producer is active then exits", producerErr == nil, probe.producer.details())
	if producerErr != nil {
		return producerErr
	}

	openFileResidueCount, openFileErr := waitForLegacyFMFixtureOpenFilesClosed(ctx, probe.agentPID, probe.root)
	probe.assertions.Record("FM closes fixture-root files", openFileErr == nil && openFileResidueCount == 0, fmt.Sprintf("fm_open_file_residue_count: %d", openFileResidueCount))
	if openFileErr != nil {
		return openFileErr
	}

	residueSample, err := processharness.SampleProcess(probe.agentPID)
	if err != nil {
		probe.assertions.Record("Agent process residue has no drift", false, errorText(err))
		return err
	}
	processResidue := legacyFMProcessResidue{Baseline: probe.baseline, End: residueSample}
	processErr := processResidue.validate()
	probe.assertions.Record("Agent process residue has no drift", processErr == nil, fmt.Sprintf("baseline_non_stdio_fds=%d end_non_stdio_fds=%d baseline_descendants=%d end_descendants=%d baseline_tcp_listeners=%d end_tcp_listeners=%d baseline_tcp6_listeners=%d end_tcp6_listeners=%d", probe.baseline.NonStdioFDCount, residueSample.NonStdioFDCount, probe.baseline.DescendantCount, residueSample.DescendantCount, probe.baseline.TCPListenerCount, residueSample.TCPListenerCount, probe.baseline.TCP6ListenerCount, residueSample.TCP6ListenerCount))
	return processErr
}
