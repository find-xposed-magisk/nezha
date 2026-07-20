package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

func TestCLI_FailedGenerationRemovesPriorEvidence(t *testing.T) {
	resultsDir := t.TempDir()
	seedStaleEvidence(t, resultsDir)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", resultsDir, "--scenario", "future-scenario"}, &stdout, &stderr, time.Now()); err == nil {
		t.Fatal("unimplemented runtime unexpectedly succeeded")
	}
	for _, name := range append(evidence.FixedEvidenceFiles()[1:], "agents") {
		if _, err := os.Stat(filepath.Join(resultsDir, name)); !os.IsNotExist(err) {
			t.Fatalf("stale artifact remains: %s (%v)", name, err)
		}
	}
}

func TestCLI_FailedGenerationRemovesPriorEvidenceWhenMetadataIsSymlink(t *testing.T) {
	resultsDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "metadata-target")
	if err := os.WriteFile(target, []byte("old metadata"), 0o600); err != nil {
		t.Fatalf("seed metadata target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(resultsDir, "metadata.json")); err != nil {
		t.Fatalf("create metadata symlink: %v", err)
	}
	for _, name := range []string{"results.json", "junit.xml"} {
		if err := os.WriteFile(filepath.Join(resultsDir, name), []byte("stale success"), 0o600); err != nil {
			t.Fatalf("seed stale artifact %s: %v", name, err)
		}
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", resultsDir, "--scenario", "future-scenario"}, &stdout, &stderr, time.Now()); err == nil {
		t.Fatal("unimplemented runtime unexpectedly succeeded")
	}
	metadataInfo, err := os.Lstat(filepath.Join(resultsDir, "metadata.json"))
	if err != nil || !metadataInfo.Mode().IsRegular() || metadataInfo.Mode().Perm() != 0o600 {
		t.Fatalf("replacement metadata is not a private regular file: mode=%v err=%v", metadataInfo, err)
	}
	for _, name := range []string{"results.json", "junit.xml"} {
		if _, err := os.Stat(filepath.Join(resultsDir, name)); !os.IsNotExist(err) {
			t.Fatalf("stale artifact remains after failed invocation: %s (%v)", name, err)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "old metadata" {
		t.Fatalf("metadata symlink target changed: %q %v", data, err)
	}
}

func TestCLI_ParseFailureRemovesPriorEvidence(t *testing.T) {
	for name, extraArgs := range map[string][]string{
		"invalid profile": {"--profile", "invalid-profile"},
		"invalid seed":    {"--profile", "pr-full", "--seed", "invalid-seed"},
	} {
		t.Run(name, func(t *testing.T) {
			resultsDir := t.TempDir()
			seedStaleEvidence(t, resultsDir)
			args := append([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--results-dir", resultsDir}, extraArgs...)
			var stdout, stderr bytes.Buffer
			if err := run(args, &stdout, &stderr, time.Now()); err == nil {
				t.Fatal("invalid CLI input unexpectedly succeeded")
			}
			for _, artifact := range evidence.FixedEvidenceFiles()[1:] {
				if _, err := os.Stat(filepath.Join(resultsDir, artifact)); !os.IsNotExist(err) {
					t.Fatalf("stale artifact remains after parse failure: %s (%v)", artifact, err)
				}
			}
		})
	}
}

func seedStaleEvidence(t *testing.T, resultsDir string) {
	t.Helper()
	for _, name := range evidence.FixedEvidenceFiles()[1:] {
		if err := os.WriteFile(filepath.Join(resultsDir, name), []byte("stale success"), 0o600); err != nil {
			t.Fatalf("seed stale artifact %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(resultsDir, "agents"), 0o700); err != nil {
		t.Fatalf("create stale agents directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resultsDir, "agents", "old.log"), []byte("old success"), 0o600); err != nil {
		t.Fatalf("seed stale agent log: %v", err)
	}
}
