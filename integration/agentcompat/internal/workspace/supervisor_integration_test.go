//go:build linux

package workspace

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

const workspaceListenerHelperEnv = "GO_WANT_WORKSPACE_LISTENER_HELPER"

func TestWorkspace_TransfersListenerToSupervisorAndRemovesResidue(t *testing.T) {
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
	owned, err := workspace.AdoptListener(listener)
	if err != nil {
		t.Fatal(err)
	}
	extraFile, err := owned.ExtraFile()
	if err != nil {
		t.Fatal(err)
	}
	supervisor := processharness.NewSupervisor(t.Context(), processharness.Spec{
		Name:             "workspace-listener",
		Path:             os.Args[0],
		Args:             []string{"-test.run=^TestWorkspaceListenerHelper$"},
		Env:              append(os.Environ(), workspaceListenerHelperEnv+"=3"),
		ExtraFiles:       []*os.File{extraFile},
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		MaxLogBytes:      1024,
		TerminateTimeout: 100 * time.Millisecond,
		KillTimeout:      time.Second,
		Readiness: func(_ processharness.Stream, line string) bool {
			return strings.Contains(line, "READY")
		},
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
	if err := supervisor.WaitReady(t.Context()); err != nil {
		t.Fatal(err)
	}

	// When
	if err := supervisor.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}

	// Then
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("workspace remains: %v", err)
	}
}

func TestWorkspaceListenerHelper(t *testing.T) {
	rawDescriptor := os.Getenv(workspaceListenerHelperEnv)
	if rawDescriptor == "" {
		return
	}
	descriptor, err := strconv.Atoi(rawDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(descriptor), "workspace-listener")
	listener, err := net.FileListener(file)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	defer listener.Close()
	fmt.Println("READY")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}
