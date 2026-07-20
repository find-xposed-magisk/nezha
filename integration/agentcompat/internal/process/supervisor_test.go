//go:build linux

package process

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisor_CleanExit(t *testing.T) {
	// Given
	supervisor := newHelperSupervisor(t.Context(), "clean", nil)

	// When
	requireNoError(t, supervisor.Start())
	requireNoError(t, supervisor.WaitReady(t.Context()))

	// Then
	requireNoError(t, supervisor.Wait(t.Context()))
}

func TestSupervisor_RunsChildWithConfiguredCredential(t *testing.T) {
	// Given
	credentialDirectory, err := os.MkdirTemp("/tmp", "agentcompat-credential-")
	requireNoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(credentialDirectory) })
	requireNoError(t, os.Chmod(credentialDirectory, 0o777))
	marker := filepath.Join(credentialDirectory, "credential.txt")
	supervisor := newHelperSupervisor(t.Context(), "credential", []string{helperMarkerEnv + "=" + marker})
	testBinary, err := os.ReadFile(os.Args[0])
	requireNoError(t, err)
	executablePath := filepath.Join(credentialDirectory, "process-helper")
	requireNoError(t, os.WriteFile(executablePath, testBinary, 0o755))
	uncredentialed := newHelperSupervisor(t.Context(), "credential", []string{helperMarkerEnv + "=" + marker})
	uncredentialed.spec.Path = executablePath
	requireNoError(t, uncredentialed.Start())
	requireNoError(t, uncredentialed.Wait(t.Context()))
	requireNoError(t, os.Remove(marker))

	supervisor.spec.Path = executablePath
	supervisor.spec.Credential = &syscall.Credential{Uid: 65534, Gid: 65534}

	// When
	if err := supervisor.Start(); err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skipf("credentialed helper execution is not permitted: %v", err)
		}
		t.Fatalf("start credentialed helper: %v", err)
	}
	requireNoError(t, supervisor.Wait(t.Context()))

	// Then
	content, err := os.ReadFile(marker)
	requireNoError(t, err)
	if strings.TrimSpace(string(content)) != "65534:65534" {
		t.Fatalf("child credential = %q, want 65534:65534", content)
	}
}

func TestSupervisor_KillsProcessTree(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "grandchild.pid")
	ctx, cancel := context.WithCancel(t.Context())
	supervisor := newHelperSupervisor(ctx, "tree", []string{helperMarkerEnv + "=" + marker})
	requireNoError(t, supervisor.Start())
	requireNoError(t, supervisor.WaitReady(t.Context()))
	grandchildPID := readPID(t, marker)
	processGroupID := supervisor.ProcessGroupID()

	// When
	cancel()
	select {
	case <-supervisor.cleanupDone:
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation did not complete process-tree cleanup")
	}

	// Then
	requirePIDGone(t, supervisor.PID())
	requirePIDGone(t, grandchildPID)
	if err := syscall.Kill(-processGroupID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d remains: %v", processGroupID, err)
	}
}

func TestSupervisor_AdoptsListener(t *testing.T) {
	// Given
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	requireNoError(t, err)
	tcpListener := listener.(*net.TCPListener)
	inheritedFile, err := tcpListener.File()
	requireNoError(t, err)
	requireNoError(t, tcpListener.Close())
	supervisor := newHelperSupervisor(t.Context(), "listener", []string{helperFDEnv + "=3"})
	supervisor.spec.ExtraFiles = []*os.File{inheritedFile}

	// When
	requireNoError(t, supervisor.Start())
	requireNoError(t, supervisor.WaitReady(t.Context()))
	sample, err := SampleProcess(supervisor.PID())
	requireNoError(t, err)

	// Then
	if sample.TCPListenerCount != 1 {
		t.Fatalf("TCP listeners = %d, want 1", sample.TCPListenerCount)
	}
	requireNoError(t, supervisor.Stop(t.Context()))
	requirePIDGone(t, supervisor.PID())
}

func TestSupervisor_RedactsLogs(t *testing.T) {
	// Given
	var output bytes.Buffer
	supervisor := newHelperSupervisor(t.Context(), "logs", nil)
	supervisor.spec.MaxLogBytes = 128
	supervisor.spec.Stdout = &output
	supervisor.spec.Stderr = &output

	// When
	requireNoError(t, supervisor.Start())
	requireNoError(t, supervisor.WaitReady(t.Context()))
	requireNoError(t, supervisor.Wait(t.Context()))

	// Then
	logged := output.String()
	if strings.Contains(logged, "top-secret") || strings.Contains(logged, "eyJsecret") {
		t.Fatalf("secret survived supervisor log redaction: %s", logged)
	}
	if output.Len() > supervisor.spec.MaxLogBytes*2 {
		t.Fatalf("combined log bytes = %d, per-stream limit = %d", output.Len(), supervisor.spec.MaxLogBytes)
	}
	if !strings.Contains(logged, truncationMarker) {
		t.Fatalf("truncation marker missing: %q", logged)
	}
}

func TestSupervisor_RecordsForcedCleanupForSIGTERMIgnoringChild(t *testing.T) {
	// Given
	resultsDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "forced-grandchild.pid")
	supervisor := newHelperSupervisor(t.Context(), "force-tree", []string{helperMarkerEnv + "=" + marker})
	requireNoError(t, supervisor.Start())
	requireNoError(t, supervisor.WaitReady(t.Context()))
	grandchildPID := readPID(t, marker)

	// When
	requireNoError(t, supervisor.Stop(t.Context()))
	receipt := NewCleanupReceipt([]CleanupRecord{supervisor.CleanupRecord()})
	receiptPath := filepath.Join(resultsDir, "cleanup.json")
	requireNoError(t, WriteCleanupReceipt(receiptPath, receipt))

	// Then
	if !supervisor.ForcedCleanup() {
		t.Fatal("forced cleanup was not recorded")
	}
	data, err := os.ReadFile(receiptPath)
	requireNoError(t, err)
	if !strings.Contains(string(data), `"forced": true`) {
		t.Fatalf("cleanup receipt = %s", data)
	}
	requirePIDGone(t, supervisor.PID())
	requirePIDGone(t, grandchildPID)
}

func TestSupervisor_InterruptSignalCleansProcessTree(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "interrupt-grandchild.pid")
	command := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
	command.Env = append(os.Environ(), helperModeEnv+"=interrupt-probe", helperMarkerEnv+"="+marker)
	output, err := command.StdoutPipe()
	requireNoError(t, err)
	command.Stderr = os.Stderr
	requireNoError(t, command.Start())
	scanner := bufio.NewScanner(output)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "PROBE_READY" {
			ready = true
			break
		}
	}
	requireNoError(t, scanner.Err())
	if !ready {
		t.Fatal("interrupt probe exited before readiness")
	}
	leaderPID := readPID(t, marker+".leader")
	grandchildPID := readPID(t, marker)

	// When
	requireNoError(t, command.Process.Signal(syscall.SIGTERM))
	requireNoError(t, command.Wait())

	// Then
	requirePIDGone(t, leaderPID)
	requirePIDGone(t, grandchildPID)
}
