//go:build linux

package workspace

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspace_RemovesWorkspace(t *testing.T) {
	// Given
	workspace, err := New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root := workspace.Root()
	// When
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	// Then
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists: %v", err)
	}
}

func TestWorkspace_RedactsLogs(t *testing.T) {
	// Given
	workspace, err := New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	// When
	logFile, err := workspace.Log("agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logFile.WriteString("Authorization: Bearer eyJsecret.secret.secret password=top-secret\n" + strings.Repeat("x", defaultLogBytes*2)); err != nil {
		t.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	// Then
	data, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "top-secret") || strings.Contains(string(data), "eyJsecret") {
		t.Fatalf("secret survived log redaction: %s", data)
	}
	if len(data) > defaultLogBytes {
		t.Fatalf("log bytes = %d, limit = %d", len(data), defaultLogBytes)
	}
}

func TestWorkspace_AdoptsListener(t *testing.T) {
	// Given
	workspace, err := New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// When
	owned, err := workspace.AdoptListener(listener)
	if err != nil {
		t.Fatal(err)
	}
	// Then
	if _, err := listener.Accept(); err == nil {
		t.Fatal("original listener retained ownership")
	}
	if owned.FileDescriptor() < 3 {
		t.Fatalf("listener FD = %d", owned.FileDescriptor())
	}
	extraFile, err := owned.ExtraFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := extraFile.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspace_BuildsBinaryInRunDirectory(t *testing.T) {
	// Given
	workspace, err := New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module example.com/workspacefixture\n\ngo 1.26.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// When
	binaryPath, err := workspace.Build(t.Context(), BuildSpec{Name: "fixture", SourceDir: source, Package: "."})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(binaryPath) != filepath.Join(workspace.Root(), "bin") {
		t.Fatalf("binary path = %s", binaryPath)
	}
	if info, err := os.Stat(binaryPath); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("binary is not executable: info=%v err=%v", info, err)
	}
}

func TestWorkspace_PreservesEvidenceWhenTrackedPIDRemains(t *testing.T) {
	// Given
	workspace, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := workspace.Root()
	if err := workspace.TrackPID(os.Getpid()); err != nil {
		t.Fatal(err)
	}

	// When
	err = workspace.Close()

	// Then
	if err == nil || !strings.Contains(err.Error(), "tracked PID") {
		t.Fatalf("close error = %v", err)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Fatalf("workspace evidence was removed: %v", statErr)
	}
	if removeErr := os.RemoveAll(root); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		t.Fatal(removeErr)
	}
}

func TestWorkspace_RetriesCleanupAfterTrackedPIDExits(t *testing.T) {
	// Given
	workspace, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := workspace.Root()
	command, input := startWorkspaceHelper(t)
	if err := workspace.TrackPID(command.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Close(); err == nil {
		t.Fatal("workspace closed while tracked PID was live")
	}

	// When
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}

	// Then
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains after retry: %v", err)
	}
}

func TestWorkspace_RejectsResourcesAfterClose(t *testing.T) {
	// Given
	workspace, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	// When / Then
	if _, err := workspace.Log("late"); err == nil {
		t.Fatal("closed workspace accepted a log")
	}
	if _, err := workspace.AdoptListener(listener); err == nil {
		t.Fatal("closed workspace accepted a listener")
	}
	if err := workspace.TrackPID(os.Getpid()); err == nil {
		t.Fatal("closed workspace accepted a PID")
	}
	if err := workspace.TrackProcessGroup(os.Getpid()); err == nil {
		t.Fatal("closed workspace accepted a process group")
	}
}
