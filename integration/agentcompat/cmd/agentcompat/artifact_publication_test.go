package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/scenario"
)

func TestCLI_ArtifactPublicationReplacesPublicFileAndFinalSymlink(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string, string)
	}{
		{"public file", func(t *testing.T, path, _ string) {
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatalf("write old file: %v", err)
			}
		}},
		{"final symlink", func(t *testing.T, path, sentinel string) {
			if err := os.Symlink(sentinel, path); err != nil {
				t.Fatalf("symlink final path: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cleanup.json")
			sentinel := filepath.Join(t.TempDir(), "sentinel")
			if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}
			test.setup(t, path, sentinel)
			if err := writeJSONArtifact(dir, "cleanup.json", map[string]bool{"passed": true}); err != nil {
				t.Fatalf("publish artifact: %v", err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("lstat artifact: %v", err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatalf("artifact mode=%v", info.Mode())
			}
			content, err := os.ReadFile(sentinel)
			if err != nil || string(content) != "unchanged" {
				t.Fatalf("outside sentinel changed: content=%q err=%v", content, err)
			}
		})
	}
}

func TestCLI_InterruptedScenarioPublicationLeavesAtomicInvalidDirectory(t *testing.T) {
	dir := t.TempDir()
	config := testCLIConfig(t, contract.ScenarioTransfer100MiB, contract.FaultTransferHash)
	paths, err := contract.NewPaths(config.Paths.NezhaSource().String(), config.Paths.AgentSource().String(), dir)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	config.Paths = paths
	if err := writeMetadata(t.Context(), config, time.Now()); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	previous := scenarioArtifactPublished
	scenarioArtifactPublished = func(name string) error {
		if name == "results.json" {
			return errors.New("injected publication interruption")
		}
		return nil
	}
	t.Cleanup(func() { scenarioArtifactPublished = previous })
	output := scenarioExecutionOutput{Result: scenario.Result{Name: contract.ScenarioTransfer100MiB, Passed: false, CleanupOK: true, Error: "transfer scenario: injected hash mismatch"}, Transfer: &scenario.TransferEvidence{WarmupUploadBytes: 65536, WarmupDownloadBytes: 65536, WarmupSHA256: "abc", WarmupDuration: time.Nanosecond, WarmupDeadlineRemaining: time.Second, WarmupQuiescent: true, OutsideRootSentinelsUnchanged: true}}
	if err := writeScenarioEvidence(config, output, time.Now()); err == nil {
		t.Fatal("publication interruption accepted")
	}
	if err := evidence.ValidateDirectory(dir); err == nil {
		t.Fatal("partial evidence directory validated")
	}
	data, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if !errors.Is(err, os.ErrNotExist) || len(data) != 0 {
		t.Fatalf("interrupted final file published: bytes=%d err=%v", len(data), err)
	}
}

func TestCLI_PrivateArtifactJoinsPrimaryAndCleanupErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")
	primaryErr := errors.New("publication hook failed")
	removeErr := errors.New("temporary removal failed")
	previousClose := privateArtifactClose
	previousRemove := privateArtifactRemove
	closeCalls := 0
	privateArtifactClose = func(file *os.File) error {
		closeCalls++
		return file.Close()
	}
	privateArtifactRemove = func(string) error { return removeErr }
	t.Cleanup(func() {
		privateArtifactClose = previousClose
		privateArtifactRemove = previousRemove
	})

	err := writePrivateArtifactWithSeam(path, []byte("payload"), func() error { return primaryErr })
	if !errors.Is(err, primaryErr) || !errors.Is(err, removeErr) {
		t.Fatalf("publication error=%v, want primary and removal errors", err)
	}
	if errors.Is(err, os.ErrClosed) || closeCalls != 1 {
		t.Fatalf("successful close repeated: calls=%d err=%v", closeCalls, err)
	}
}

func TestCLI_PrivateArtifactJoinsCloseAndRemoveErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")
	closeErr := errors.New("temporary close failed")
	removeErr := errors.New("temporary removal failed")
	previousClose := privateArtifactClose
	previousRemove := privateArtifactRemove
	closeCalls := 0
	privateArtifactClose = func(file *os.File) error {
		closeCalls++
		if err := file.Close(); err != nil {
			return err
		}
		return closeErr
	}
	privateArtifactRemove = func(string) error { return removeErr }
	t.Cleanup(func() {
		privateArtifactClose = previousClose
		privateArtifactRemove = previousRemove
	})

	err := writePrivateArtifactWithSeam(path, []byte("payload"), func() error { return nil })
	if !errors.Is(err, closeErr) || !errors.Is(err, removeErr) {
		t.Fatalf("publication error=%v, want close and removal errors", err)
	}
	if errors.Is(err, os.ErrClosed) || closeCalls != 1 {
		t.Fatalf("close failure retried: calls=%d err=%v", closeCalls, err)
	}
}

func TestCLI_PrivateArtifactRemovesTemporaryFileAfterHookFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.json")
	err := writePrivateArtifactWithSeam(path, []byte("payload"), func() error {
		return errors.New("publication hook failed")
	})
	if err == nil {
		t.Fatal("publication hook failure accepted")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read artifact directory: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary artifact survived hook failure: %v", entries)
	}
}
