//go:build linux

package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
)

const (
	helperModeEnv   = "NEZHA_AGENTCOMPAT_PROCESS_HELPER"
	helperMarkerEnv = "NEZHA_AGENTCOMPAT_PROCESS_MARKER"
	helperFDEnv     = "NEZHA_AGENTCOMPAT_PROCESS_FD"
)

func TestProcessHelper(t *testing.T) {
	switch os.Getenv(helperModeEnv) {
	case "":
		return
	case "clean":
		fmt.Println("READY")
	case "credential":
		marker := os.Getenv(helperMarkerEnv)
		if err := os.WriteFile(marker, []byte(fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())), 0o600); err != nil {
			t.Fatal(err)
		}
	case "block":
		fmt.Println("READY")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "tree":
		runTreeHelper(t, false)
	case "force-tree":
		runTreeHelper(t, true)
	case "grandchild":
		runGrandchildHelper(t)
	case "ignore-term-grandchild":
		signal.Ignore(syscall.SIGTERM)
		runGrandchildHelper(t)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("READY")
		waitForSignal(syscall.SIGINT)
	case "listener":
		runListenerHelper(t)
	case "logs":
		fmt.Println("READY")
		fmt.Println("Authorization: Bearer eyJsecret.secret.secret password=top-secret")
		fmt.Println(strings.Repeat("x", 1024))
	case "interrupt-probe":
		runInterruptProbeHelper(t)
	default:
		t.Fatalf("unknown helper mode %q", os.Getenv(helperModeEnv))
	}
}

func runInterruptProbeHelper(t *testing.T) {
	t.Helper()
	marker := os.Getenv(helperMarkerEnv)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	supervisor := newHelperSupervisor(ctx, "tree", []string{helperMarkerEnv + "=" + marker})
	requireNoError(t, supervisor.Start())
	requireNoError(t, supervisor.WaitReady(t.Context()))
	grandchildPID := readPID(t, marker)
	if err := os.WriteFile(marker+".leader", []byte(strconv.Itoa(supervisor.PID())), 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("PROBE_READY")
	<-ctx.Done()
	select {
	case <-supervisor.cleanupDone:
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation did not complete process-tree cleanup")
	}
	requirePIDGone(t, supervisor.PID())
	requirePIDGone(t, grandchildPID)
}

func runTreeHelper(t *testing.T, ignoreTermination bool) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	childMode := "grandchild"
	if ignoreTermination {
		childMode = "ignore-term-grandchild"
		signal.Ignore(syscall.SIGTERM)
	}
	child.Env = append(os.Environ(), helperModeEnv+"="+childMode)
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if ignoreTermination {
		waitForSignal(syscall.SIGINT)
		_ = child.Wait()
		return
	}
	waitForSignal(syscall.SIGTERM)
	_ = child.Wait()
}

func runGrandchildHelper(t *testing.T) {
	t.Helper()
	marker := os.Getenv(helperMarkerEnv)
	if marker == "" {
		t.Fatal("helper marker is empty")
	}
	if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("READY")
	waitForSignal(syscall.SIGTERM)
}

func runListenerHelper(t *testing.T) {
	t.Helper()
	descriptor, err := strconv.Atoi(os.Getenv(helperFDEnv))
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(descriptor), "inherited-listener")
	listener, err := net.FileListener(file)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	defer listener.Close()
	fmt.Println("READY")
	waitForSignal(syscall.SIGTERM)
}

func waitForSignal(expected os.Signal) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, expected)
	defer signal.Stop(signals)
	<-signals
}

func newHelperSupervisor(ctx context.Context, mode string, environment []string) *Supervisor {
	return NewSupervisor(ctx, Spec{
		Name:             "helper-" + mode,
		Path:             os.Args[0],
		Args:             []string{"-test.run=^TestProcessHelper$"},
		Env:              append(append(os.Environ(), helperModeEnv+"="+mode), environment...),
		MaxLogBytes:      1024,
		TerminateTimeout: 100 * time.Millisecond,
		KillTimeout:      time.Second,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		Readiness: func(_ Stream, line string) bool {
			return strings.Contains(line, "READY")
		},
	})
}

func startBlockingHelper(t *testing.T) (*exec.Cmd, func()) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	command.Env = append(os.Environ(), helperModeEnv+"=block")
	input, err := command.StdinPipe()
	requireNoError(t, err)
	output, err := command.StdoutPipe()
	requireNoError(t, err)
	requireNoError(t, command.Start())
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "READY" {
		t.Fatalf("helper readiness = %q, err = %v", scanner.Text(), scanner.Err())
	}
	return command, func() { _ = input.Close() }
}

func startCleanHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	command.Env = append(os.Environ(), helperModeEnv+"=clean")
	requireNoError(t, command.Start())
	return command
}

func reapHelper(command *exec.Cmd) {
	if command.ProcessState == nil {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	requireNoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	requireNoError(t, err)
	return pid
}

func requirePIDGone(t *testing.T, pid int) {
	t.Helper()
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PID %d remains: %v", pid, err)
	}
}

func containsPID(pids []int, target int) bool {
	for _, pid := range pids {
		if pid == target {
			return true
		}
	}
	return false
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
