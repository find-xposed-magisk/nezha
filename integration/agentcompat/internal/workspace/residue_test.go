//go:build linux

package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestWorkspace_PreservesEvidenceWhenProcessGroupRemains(t *testing.T) {
	// Given
	workspace, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := workspace.Root()
	command, input := startWorkspaceHelper(t)
	if err := workspace.TrackProcessGroup(command.Process.Pid); err != nil {
		t.Fatal(err)
	}

	// When
	err = workspace.Close()

	// Then
	if err == nil || !strings.Contains(err.Error(), "process group") {
		t.Fatalf("close error = %v", err)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Fatalf("workspace evidence was removed: %v", statErr)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains after group exit: %v", err)
	}
}

func TestWorkspaceHelper(t *testing.T) {
	if os.Getenv("GO_WANT_WORKSPACE_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func startWorkspaceHelper(t *testing.T) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestWorkspaceHelper$")
	command.Env = append(os.Environ(), "GO_WANT_WORKSPACE_HELPER=1")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return command, input
}
