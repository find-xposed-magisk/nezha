//go:build linux && agentcompat

package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

const (
	workspaceExitHelperModeEnv   = "NEZHA_AGENTCOMPAT_WORKSPACE_EXIT_HELPER"
	workspaceExitHelperMarkerEnv = "NEZHA_AGENTCOMPAT_WORKSPACE_EXIT_MARKER"
)

func TestWorkspace_SupervisorExitedPrecedesProcessGroupAndListenerCleanup(t *testing.T) {
	// Given
	workspace, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := workspace.Root()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ownedListener, err := workspace.AdoptListener(listener)
	if err != nil {
		t.Fatal(err)
	}
	inheritedFile, err := ownedListener.ExtraFile()
	if err != nil {
		t.Fatal(err)
	}
	descendantMarker := filepath.Join(t.TempDir(), "descendant.pid")
	supervisor := processharness.NewSupervisor(t.Context(), processharness.Spec{
		Name:             "workspace-exited-semantics",
		Path:             os.Args[0],
		Args:             []string{"-test.run=^TestWorkspaceExitHelper$"},
		Env:              append(os.Environ(), workspaceExitHelperModeEnv+"=leader", workspaceExitHelperMarkerEnv+"="+descendantMarker),
		ExtraFiles:       []*os.File{inheritedFile},
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		MaxLogBytes:      1024,
		TerminateTimeout: time.Second,
		KillTimeout:      time.Second,
	})
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	if err := workspace.TrackPID(supervisor.PID()); err != nil {
		t.Fatal(err)
	}
	if err := workspace.TrackProcessGroup(supervisor.ProcessGroupID()); err != nil {
		t.Fatal(err)
	}
	if err := ownedListener.Close(); err != nil {
		t.Fatal(err)
	}
	processGroupID := supervisor.ProcessGroupID()

	// When
	select {
	case <-supervisor.Exited():
	case <-time.After(2 * time.Second):
		t.Fatal("leader did not exit")
	}
	descendantPID := readWorkspaceExitHelperPID(t, descendantMarker)

	// Then
	select {
	case <-supervisor.CleanupDoneForTest():
		t.Fatal("cleanup completed before Stop")
	default:
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(descendantPID))); err != nil {
		t.Fatalf("descendant exited before cleanup: %v", err)
	}
	if descendantProcessGroupID := readProcessGroupID(t, descendantPID); descendantProcessGroupID != processGroupID {
		t.Fatalf("descendant process group = %d, want %d", descendantProcessGroupID, processGroupID)
	}
	if err := syscall.Kill(-processGroupID, 0); err != nil {
		t.Fatalf("process group exited before cleanup: %v", err)
	}
	requireProcessHoldsSocket(t, descendantPID, ownedListener.inode)
	listenerPresent, err := listenerInodePresent(ownedListener.inode)
	if err != nil {
		t.Fatal(err)
	}
	if !listenerPresent {
		t.Fatal("descendant listener disappeared before cleanup")
	}
	if err := workspace.Close(); err == nil {
		t.Fatal("workspace closed before descendant cleanup")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("workspace disappeared before cleanup: %v", err)
	}

	stopContext, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := supervisor.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-supervisor.CleanupDoneForTest():
	case <-time.After(time.Second):
		t.Fatal("cleanup completion signal did not close after Stop")
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(descendantPID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant remains after Stop: %v", err)
	}
	if err := syscall.Kill(-processGroupID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group remains after Stop: %v", err)
	}
	listenerPresent, err = listenerInodePresent(ownedListener.inode)
	if err != nil {
		t.Fatal(err)
	}
	if listenerPresent {
		t.Fatal("listener remains after Stop")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains after cleanup: %v", err)
	}
}

func TestWorkspaceExitHelper(t *testing.T) {
	switch os.Getenv(workspaceExitHelperModeEnv) {
	case "":
		return
	case "leader":
		runWorkspaceExitLeader(t)
	case "descendant":
		runWorkspaceExitDescendant(t)
	default:
		t.Fatalf("unknown workspace exit helper mode %q", os.Getenv(workspaceExitHelperModeEnv))
	}
}

func runWorkspaceExitLeader(t *testing.T) {
	t.Helper()
	listenerFile := os.NewFile(3, "workspace-exit-listener")
	child := exec.Command(os.Args[0], "-test.run=^TestWorkspaceExitHelper$")
	child.Env = append(os.Environ(), workspaceExitHelperModeEnv+"=descendant", workspaceExitHelperMarkerEnv+"="+os.Getenv(workspaceExitHelperMarkerEnv))
	child.ExtraFiles = []*os.File{listenerFile}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := listenerFile.Close(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "DESCENDANT_READY" {
		t.Fatalf("descendant readiness = %q, err = %v", scanner.Text(), scanner.Err())
	}
	fmt.Println("READY")
}

func runWorkspaceExitDescendant(t *testing.T) {
	t.Helper()
	listenerFile := os.NewFile(3, "workspace-exit-listener")
	listener, err := net.FileListener(listenerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := listenerFile.Close(); err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.WriteFile(os.Getenv(workspaceExitHelperMarkerEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("DESCENDANT_READY")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}

func readWorkspaceExitHelperPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func readProcessGroupID(t *testing.T, pid int) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		t.Fatal(err)
	}
	commandEnd := strings.LastIndex(string(data), ") ")
	if commandEnd < 0 {
		t.Fatalf("invalid process stat for PID %d", pid)
	}
	fields := strings.Fields(string(data)[commandEnd+2:])
	if len(fields) < 3 {
		t.Fatalf("process stat for PID %d has %d fields after command", pid, len(fields))
	}
	processGroupID, err := strconv.Atoi(fields[2])
	if err != nil {
		t.Fatal(err)
	}
	return processGroupID
}

func requireProcessHoldsSocket(t *testing.T, pid int, inode uint64) {
	t.Helper()
	descriptorDirectory := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	descriptors, err := os.ReadDir(descriptorDirectory)
	if err != nil {
		t.Fatal(err)
	}
	wantedTarget := fmt.Sprintf("socket:[%d]", inode)
	for _, descriptor := range descriptors {
		target, err := os.Readlink(filepath.Join(descriptorDirectory, descriptor.Name()))
		if err == nil && target == wantedTarget {
			return
		}
	}
	t.Fatalf("PID %d does not hold %s", pid, wantedTarget)
}
