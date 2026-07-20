//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package evidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvidence_RejectsDedicatedArtifactWithPublicMode(t *testing.T) {
	dir := t.TempDir()
	writeExecutableEvidence(t, dir, "pr-full", "transfer-100mib", true, true, true)
	path := filepath.Join(dir, "transfer.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write transfer artifact: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod transfer artifact: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Errorf("restore transfer artifact mode: %v", err)
		}
	})
	if err := ValidateDirectory(dir); err == nil {
		t.Fatal("public dedicated artifact mode accepted")
	}
}
