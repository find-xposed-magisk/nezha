package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestEvidence_ValidateDirectoryRejectsPublicRootOrEvidenceFile(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "root", path: ""},
		{name: "metadata", path: "metadata.json"},
		{name: "results", path: "results.json"},
		{name: "junit", path: "junit.xml"},
		{name: "cleanup", path: "cleanup.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeExecutableEvidence(t, dir, "pr-full", contract.ScenarioRegistrationConfigExec, true, true, true)
			path := dir
			if test.path != "" {
				path = filepath.Join(dir, test.path)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatalf("chmod fixture: %v", err)
			}
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("public evidence mode accepted")
			}
		})
	}
}

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
