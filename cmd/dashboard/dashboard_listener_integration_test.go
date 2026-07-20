//go:build agentcompat

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

const (
	dashboardListenerHelperModeEnv = "NEZHA_AGENTCOMPAT_LISTENER_HELPER"
	dashboardListenerHelperKindEnv = "NEZHA_AGENTCOMPAT_LISTENER_HELPER_KIND"
)

func TestDashboardListener_UsesDefaultListen(t *testing.T) {
	// Given
	t.Setenv(dashboardHTTPListenerFDEnv, "")
	t.Setenv(dashboardHTTPSListenerFDEnv, "")

	// When
	listener, err := openDashboardListener("tcp", "127.0.0.1:0", dashboardHTTPListener)

	// Then
	if err != nil {
		t.Fatalf("open default dashboard listener: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close default dashboard listener: %v", err)
		}
	})
	address := listener.Addr().(*net.TCPAddr)
	if !address.IP.IsLoopback() {
		t.Fatalf("default listener address = %s, want loopback", address)
	}
}

func TestDashboardListener_AdoptsInheritedLoopback(t *testing.T) {
	tests := []struct {
		name                string
		environmentVariable string
		kind                dashboardListenerKind
	}{
		{name: "HTTP", environmentVariable: dashboardHTTPListenerFDEnv, kind: dashboardHTTPListener},
		{name: "HTTPS", environmentVariable: dashboardHTTPSListenerFDEnv, kind: dashboardHTTPSListener},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			inherited := newDashboardTCPListener(t, "127.0.0.1:0")
			inheritedFile, err := inherited.File()
			if err != nil {
				t.Fatalf("duplicate inherited listener for helper: %v", err)
			}
			t.Cleanup(func() {
				if err := inheritedFile.Close(); err != nil {
					t.Errorf("close helper listener file: %v", err)
				}
			})
			command := exec.Command(os.Args[0], "-test.run=^TestDashboardListenerAdoptionHelper$")
			command.ExtraFiles = []*os.File{inheritedFile}
			command.Env = append(os.Environ(),
				dashboardListenerHelperModeEnv+"=1",
				dashboardListenerHelperKindEnv+"="+string(test.kind),
				test.environmentVariable+"=3",
			)

			// When
			output, err := command.Output()

			// Then
			if err != nil {
				t.Fatalf("run inherited listener helper: %v", err)
			}
			var adoptedAddress string
			var adoptedInode uint64
			if _, err := fmt.Sscanf(string(output), "%s %d", &adoptedAddress, &adoptedInode); err != nil {
				t.Fatalf("parse helper output %q: %v", output, err)
			}
			if want := inherited.Addr().String(); adoptedAddress != want {
				t.Fatalf("adopted listener address = %q, want %q", adoptedAddress, want)
			}
			if want := dashboardTCPListenerInode(t, inherited); adoptedInode != want {
				t.Fatalf("adopted listener inode = %d, want %d", adoptedInode, want)
			}
			t.Logf("adopted address=%s inode=%d", adoptedAddress, adoptedInode)
		})
	}
}

func TestDashboardListenerAdoptionHelper(t *testing.T) {
	if os.Getenv(dashboardListenerHelperModeEnv) != "1" {
		return
	}
	kind := dashboardListenerKind(os.Getenv(dashboardListenerHelperKindEnv))
	listener, err := openDashboardListener("tcp", "127.0.0.1:1", kind)
	if err != nil {
		t.Fatalf("adopt inherited dashboard listener: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close adopted dashboard listener: %v", err)
		}
	})
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		t.Fatalf("adopted listener type = %T, want *net.TCPListener", listener)
	}
	fmt.Printf("%s %d\n", listener.Addr(), dashboardTCPListenerInode(t, tcpListener))
}

func TestDashboardListener_RejectsPipeFD(t *testing.T) {
	// Given
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, syscall.EBADF) {
			t.Errorf("close pipe reader: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Errorf("close pipe writer: %v", err)
		}
	})
	t.Setenv(dashboardHTTPListenerFDEnv, strconv.FormatUint(uint64(reader.Fd()), 10))

	// When
	listener, err := openDashboardListener("tcp", "127.0.0.1:0", dashboardHTTPListener)

	// Then
	if listener != nil {
		t.Cleanup(func() { _ = listener.Close() })
		t.Fatal("pipe FD produced a dashboard listener")
	}
	requireDashboardListenerError(t, err, errDashboardListenerTCP)
}

func TestDashboardListener_RejectsNonLoopback(t *testing.T) {
	// Given
	inherited := newDashboardTCPListener(t, "0.0.0.0:0")
	setDashboardInheritedListenerFD(t, dashboardHTTPListenerFDEnv, inherited)

	// When
	listener, err := openDashboardListener("tcp", "127.0.0.1:0", dashboardHTTPListener)

	// Then
	if listener != nil {
		t.Cleanup(func() { _ = listener.Close() })
		t.Fatal("non-loopback FD produced a dashboard listener")
	}
	requireDashboardListenerError(t, err, errDashboardListenerLoopback)
}

func newDashboardTCPListener(t *testing.T, address string) *net.TCPListener {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen on %s: %v", address, err)
	}
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		_ = listener.Close()
		t.Fatalf("listener type = %T, want *net.TCPListener", listener)
	}
	t.Cleanup(func() {
		if err := tcpListener.Close(); err != nil {
			t.Errorf("close inherited listener fixture: %v", err)
		}
	})
	return tcpListener
}

func setDashboardInheritedListenerFD(t *testing.T, environmentVariable string, listener *net.TCPListener) {
	t.Helper()
	rawConnection, err := listener.SyscallConn()
	if err != nil {
		t.Fatalf("get listener syscall connection: %v", err)
	}
	var inheritedFD int
	var duplicateError error
	if err := rawConnection.Control(func(fd uintptr) {
		inheritedFD, duplicateError = syscall.Dup(int(fd))
	}); err != nil {
		t.Fatalf("access listener FD: %v", err)
	}
	if duplicateError != nil {
		t.Fatalf("duplicate listener FD: %v", duplicateError)
	}
	t.Cleanup(func() {
		if err := syscall.Close(inheritedFD); err != nil && !errors.Is(err, syscall.EBADF) {
			t.Errorf("close duplicated listener FD: %v", err)
		}
	})
	t.Setenv(environmentVariable, strconv.Itoa(inheritedFD))
}

func dashboardTCPListenerInode(t *testing.T, listener *net.TCPListener) uint64 {
	t.Helper()
	rawConnection, err := listener.SyscallConn()
	if err != nil {
		t.Fatalf("get listener syscall connection: %v", err)
	}
	var stat syscall.Stat_t
	var statError error
	if err := rawConnection.Control(func(fd uintptr) {
		statError = syscall.Fstat(int(fd), &stat)
	}); err != nil {
		t.Fatalf("access listener FD: %v", err)
	}
	if statError != nil {
		t.Fatalf("stat listener FD: %v", statError)
	}
	return stat.Ino
}

func requireDashboardListenerError(t *testing.T, err, cause error) {
	t.Helper()
	if err == nil {
		t.Fatal("openDashboardListener error = nil, want typed error")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("openDashboardListener error = %v, want cause %v", err, cause)
	}
	var listenerError *dashboardListenerError
	if !errors.As(err, &listenerError) {
		t.Fatalf("openDashboardListener error type = %T, want *dashboardListenerError", err)
	}
}
