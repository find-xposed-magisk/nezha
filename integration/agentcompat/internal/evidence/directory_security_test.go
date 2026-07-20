package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestEvidence_ValidateDirectoryRejectsUnexpectedPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		mode os.FileMode
	}{
		{name: "payload", path: "payload.bin", mode: 0o600},
		{name: "executable", path: "agentcompat", mode: 0o700},
		{name: "nested log", path: "agents/nested/agent.log", mode: 0o600},
		{name: "unknown agent file", path: "agents/agent.bin", mode: 0o600},
		{name: "unknown directory", path: "unexpected/file.log", mode: 0o600},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, true, true, true)
			path := filepath.Join(dir, test.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("mkdir unexpected parent: %v", err)
			}
			if err := os.WriteFile(path, []byte("unexpected"), test.mode); err != nil {
				t.Fatalf("write unexpected file: %v", err)
			}
			if err := ValidateDirectory(dir); err == nil || !strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("unexpected path rejection err=%v", err)
			}
		})
	}
}

func TestEvidence_ValidateDirectoryRejectsSymlinkReplacementOutsideRoot(t *testing.T) {
	// Given
	dir := t.TempDir()
	writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, true, true, true)
	outside := filepath.Join(t.TempDir(), "outside-metadata.json")
	if err := os.WriteFile(outside, []byte(`{"profile":"outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(dir, "metadata.json")
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, metadata); err != nil {
		t.Fatal(err)
	}

	// When
	err := ValidateDirectory(dir)

	// Then
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink replacement error=%v", err)
	}
}

func TestEvidence_ValidateDirectoryRejectsResultsRootSymlink(t *testing.T) {
	// Given
	resultsDir := t.TempDir()
	writeExecutableEvidence(t, resultsDir, "pr-full", contract.ScenarioRegistrationConfigExec, true, true, true)
	symlink := filepath.Join(t.TempDir(), "results-link")
	if err := os.Symlink(resultsDir, symlink); err != nil {
		t.Fatal(err)
	}

	// When
	err := ValidateDirectory(symlink)

	// Then
	if err == nil || !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("results root symlink error=%v", err)
	}
}

func TestEvidence_ValidateSnapshotUsesCapturedBytesAfterDiskMutation(t *testing.T) {
	// Given
	dir := t.TempDir()
	writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, true, true, true)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	snapshot, err := scanDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	writeEvidenceFile(t, dir, "metadata.json", `{}`)
	writeEvidenceFile(t, dir, "results.json", `{}`)
	writeEvidenceFile(t, dir, "junit.xml", `<testsuite name="replaced"></testsuite>`)
	writeEvidenceFile(t, dir, "cleanup.json", `{}`)

	// When
	err = validateSnapshot(snapshot)

	// Then
	if err != nil {
		t.Fatalf("captured evidence was changed by later disk mutation: %v", err)
	}
}
