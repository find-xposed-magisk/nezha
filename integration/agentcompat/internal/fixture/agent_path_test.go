package fixture

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixture_AgentPathContained(t *testing.T) {
	// Given
	root := newTestAgentRoot(t, "agent-alpha")

	// When
	path, err := root.Path("documents/report.txt")

	// Then
	requireNoFixtureError(t, err)
	if !filepath.IsAbs(path.String()) {
		t.Fatalf("agent path is not absolute: %q", path.String())
	}
	if path.Relative() != filepath.FromSlash("documents/report.txt") {
		t.Fatalf("relative path = %q", path.Relative())
	}
	assertContainedPath(t, root.Absolute(), path.String())
	info, err := os.Lstat(filepath.Join(root.Absolute(), "documents"))
	requireNoFixtureError(t, err)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("fixture parent mode = %s", info.Mode())
	}
}

func TestFixture_AgentPathRejectsAbsolute(t *testing.T) {
	// Given
	root := newTestAgentRoot(t, "agent-absolute")

	// When
	_, err := root.Path(filepath.Join(t.TempDir(), "outside.txt"))

	// Then
	assertPathRejection(t, err, PathRejectionAbsolute)
}

func TestFixture_AgentPathRejectsParentEscape(t *testing.T) {
	root := newTestAgentRoot(t, "agent-parent")
	for _, candidate := range []string{"../outside.txt", "inside/../outside.txt"} {
		t.Run(candidate, func(t *testing.T) {
			_, err := root.Path(candidate)
			assertPathRejection(t, err, PathRejectionParent)
		})
	}
}

func TestFixture_AgentPathRejectsVolumeOrSeparatorEscape(t *testing.T) {
	root := newTestAgentRoot(t, "agent-volume")
	tests := []struct {
		name      string
		candidate string
		reason    PathRejectionReason
	}{
		{name: "drive absolute", candidate: `C:\outside.txt`, reason: PathRejectionVolume},
		{name: "drive relative", candidate: `C:outside.txt`, reason: PathRejectionVolume},
		{name: "UNC", candidate: `\\server\share\outside.txt`, reason: PathRejectionVolume},
		{name: "alternate separator", candidate: `inside\outside.txt`, reason: PathRejectionSeparator},
		{name: "empty component", candidate: "inside//outside.txt", reason: PathRejectionSeparator},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := root.Path(test.candidate)
			assertPathRejection(t, err, test.reason)
		})
	}
}

func TestFixture_AgentPathRejectsDestructiveRoot(t *testing.T) {
	// Given
	root := newTestAgentRoot(t, "agent-destructive")

	// When
	_, err := root.DestructivePath(".")

	// Then
	assertPathRejection(t, err, PathRejectionDestructiveRoot)
}

func TestFixture_AgentPathRejectsSymlinkParent(t *testing.T) {
	// Given
	root := newTestAgentRoot(t, "agent-symlink")
	outside := t.TempDir()
	requireNoFixtureError(t, os.Symlink(outside, filepath.Join(root.Absolute(), "linked")))

	// When
	_, err := root.Path("linked/file.txt")

	// Then
	assertPathRejection(t, err, PathRejectionSymlinkParent)
}

func TestFixture_AgentPathRejectsCleanedEscape(t *testing.T) {
	// Given
	root := newTestAgentRoot(t, "agent-cleaned-escape")

	// When
	_, err := root.Path("documents/../../outside.txt")

	// Then
	assertPathRejection(t, err, PathRejectionParent)
}

func TestFixture_AgentPathRejectsEmpty(t *testing.T) {
	root := newTestAgentRoot(t, "agent-empty")
	for _, candidate := range []string{"", "   "} {
		t.Run(candidate, func(t *testing.T) {
			_, err := root.Path(candidate)
			assertPathRejection(t, err, PathRejectionEmpty)
		})
	}
}

func TestFixture_AgentPathRejectsSymlinkRootParent(t *testing.T) {
	// Given
	realParent := t.TempDir()
	symlinkParent := filepath.Join(t.TempDir(), "fixture-parent")
	requireNoFixtureError(t, os.Symlink(realParent, symlinkParent))

	// When
	_, err := NewAgentRoot(symlinkParent, "agent-symlink-root")

	// Then
	if err == nil {
		t.Fatal("symlink fixture parent was accepted")
	}
}

func TestFixture_AgentPathRejectsADS(t *testing.T) {
	root := newTestAgentRoot(t, "agent-ads")
	candidate := "documents/report.txt:secret-value"
	_, err := root.Path(candidate)
	assertPathRejection(t, err, PathRejectionADS)
	if strings.Contains(err.Error(), candidate) {
		t.Fatal("path rejection exposed the candidate path")
	}
}

func TestFixture_OutsideRootSentinelUnchanged(t *testing.T) {
	// Given
	parent := t.TempDir()
	sentinel := filepath.Join(parent, "outside-sentinel")
	requireNoFixtureError(t, os.WriteFile(sentinel, []byte("unchanged"), 0o600))
	root, err := NewAgentRoot(parent, "agent-sentinel")
	requireNoFixtureError(t, err)

	// When
	_, rejectionErr := root.Path("../outside-sentinel")

	// Then
	assertPathRejection(t, rejectionErr, PathRejectionParent)
	content, err := os.ReadFile(sentinel)
	requireNoFixtureError(t, err)
	if string(content) != "unchanged" {
		t.Fatalf("outside-root sentinel changed: %q", content)
	}
}

func TestFixture_CreatesDistinctPerAgentRoots(t *testing.T) {
	// Given
	parent := t.TempDir()

	// When
	first, err := NewAgentRoot(parent, "agent-first")
	requireNoFixtureError(t, err)
	second, err := NewAgentRoot(parent, "agent-second")
	requireNoFixtureError(t, err)

	// Then
	if first.Absolute() == second.Absolute() {
		t.Fatal("per-Agent fixture roots are shared")
	}
	assertContainedPath(t, parent, first.Absolute())
	assertContainedPath(t, parent, second.Absolute())
}

func newTestAgentRoot(t *testing.T, agentID string) AgentRoot {
	t.Helper()
	root, err := NewAgentRoot(t.TempDir(), agentID)
	requireNoFixtureError(t, err)
	return root
}

func assertContainedPath(t *testing.T, root, candidate string) {
	t.Helper()
	relative, err := filepath.Rel(root, candidate)
	requireNoFixtureError(t, err)
	if relative == ".." || filepath.IsAbs(relative) || (len(relative) > 3 && relative[:3] == ".."+string(filepath.Separator)) {
		t.Fatalf("path %q escaped root %q", candidate, root)
	}
}

func assertPathRejection(t *testing.T, err error, reason PathRejectionReason) {
	t.Helper()
	var pathError *AgentPathError
	if !errors.As(err, &pathError) {
		t.Fatalf("expected AgentPathError, got %v", err)
	}
	if pathError.Reason != reason {
		t.Fatalf("rejection reason = %q, want %q", pathError.Reason, reason)
	}
	if pathError.Error() == "" {
		t.Fatal("path rejection error is empty")
	}
}

func requireNoFixtureError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
