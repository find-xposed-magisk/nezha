//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package evidence

import (
	"os"
	"path/filepath"
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
			restoredMode := os.FileMode(0o600)
			if test.path == "" {
				restoredMode = 0o700
			}
			t.Cleanup(func() {
				if err := os.Chmod(path, restoredMode); err != nil {
					t.Errorf("restore fixture mode: %v", err)
				}
			})
			if err := ValidateDirectory(dir); err == nil {
				t.Fatal("public evidence mode accepted")
			}
		})
	}
}
