//go:build linux

package process

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestSampler_ReadsRSS(t *testing.T) {
	// Given / When
	sample, err := SampleProcess(os.Getpid())

	// Then
	requireNoError(t, err)
	if sample.RSSBytes == 0 {
		t.Fatal("RSS is zero")
	}
}

func TestSampler_CountsDescendants(t *testing.T) {
	// Given
	child, closeInput := startBlockingHelper(t)
	defer closeInput()
	defer reapHelper(child)

	// When
	sample, err := SampleProcess(os.Getpid())

	// Then
	requireNoError(t, err)
	if !containsPID(sample.DescendantPIDs, child.Process.Pid) {
		t.Fatalf("descendants = %v, want PID %d", sample.DescendantPIDs, child.Process.Pid)
	}
}

func TestSampler_CountsNonStdioFDs(t *testing.T) {
	// Given
	baseline, err := SampleProcess(os.Getpid())
	requireNoError(t, err)
	file, err := os.Open("/proc/self/status")
	requireNoError(t, err)
	t.Cleanup(func() { _ = file.Close() })

	// When
	sample, err := SampleProcess(os.Getpid())

	// Then
	requireNoError(t, err)
	if sample.NonStdioFDCount != baseline.NonStdioFDCount+1 {
		t.Fatalf("non-stdio FDs = %d, baseline = %d", sample.NonStdioFDCount, baseline.NonStdioFDCount)
	}
}

func TestSampler_CountsTCPListeners(t *testing.T) {
	// Given
	baseline, err := SampleProcess(os.Getpid())
	requireNoError(t, err)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	requireNoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	// When
	sample, err := SampleProcess(os.Getpid())

	// Then
	requireNoError(t, err)
	if sample.TCPListenerCount != baseline.TCPListenerCount+1 {
		t.Fatalf("TCP listeners = %d, baseline = %d", sample.TCPListenerCount, baseline.TCPListenerCount)
	}
}

func TestSampler_CountsTCP6Listeners(t *testing.T) {
	// Given
	baseline, err := SampleProcess(os.Getpid())
	requireNoError(t, err)
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback listener unavailable: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// When
	sample, err := SampleProcess(os.Getpid())

	// Then
	requireNoError(t, err)
	if sample.TCP6ListenerCount != baseline.TCP6ListenerCount+1 {
		t.Fatalf("TCP6 listeners = %d, baseline = %d", sample.TCP6ListenerCount, baseline.TCP6ListenerCount)
	}
}

func TestSampler_CollectsFiveSampleWindow(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	// When
	window, err := SampleWindow(ctx, WindowSpec{PID: os.Getpid(), Interval: time.Millisecond})

	// Then
	requireNoError(t, err)
	if len(window.Samples) != 5 {
		t.Fatalf("samples = %d, want 5", len(window.Samples))
	}
}

func TestSampleWindow_InvokesObserverAfterEachSuccessfulAppend(t *testing.T) {
	// Given
	observed := make([]Sample, 0, 5)

	// When
	window, err := SampleWindow(t.Context(), WindowSpec{PID: os.Getpid(), Interval: time.Millisecond, ObserveSample: func(_ context.Context, sample Sample) error {
		observed = append(observed, sample)
		return nil
	}})

	// Then
	requireNoError(t, err)
	if len(window.Samples) != 5 || len(observed) != 5 {
		t.Fatalf("samples = %d, observed = %d, want 5", len(window.Samples), len(observed))
	}
	for index := range window.Samples {
		if !reflect.DeepEqual(window.Samples[index], observed[index]) {
			t.Fatalf("sample %d was not observed after append", index+1)
		}
	}
}

func TestSampleWindow_ReturnsAppendedSampleWhenObserverFails(t *testing.T) {
	// Given
	observerErr := errors.New("observer failed")
	calls := 0

	// When
	window, err := SampleWindow(t.Context(), WindowSpec{PID: os.Getpid(), Interval: time.Millisecond, ObserveSample: func(context.Context, Sample) error {
		calls++
		return observerErr
	}})

	// Then
	if !errors.Is(err, observerErr) {
		t.Fatalf("observer error = %v, want %v", err, observerErr)
	}
	if calls != 1 || len(window.Samples) != 1 {
		t.Fatalf("calls = %d, samples = %d, want one appended sample", calls, len(window.Samples))
	}
}

func TestSampler_RejectsVanishedPIDDuringWindow(t *testing.T) {
	// Given
	child := startCleanHelper(t)
	requireNoError(t, child.Wait())

	// When
	_, err := SampleWindow(t.Context(), WindowSpec{PID: child.Process.Pid, Interval: time.Millisecond})

	// Then
	if err == nil {
		t.Fatal("vanished PID was accepted")
	}
}

func TestSampler_AllowsExplicitlyTerminatedPID(t *testing.T) {
	// Given
	child := startCleanHelper(t)
	requireNoError(t, child.Wait())

	// When
	window, err := SampleWindow(t.Context(), WindowSpec{PID: child.Process.Pid, Interval: time.Millisecond, AllowTerminated: true})

	// Then
	requireNoError(t, err)
	if len(window.Samples) != 0 {
		t.Fatalf("samples = %d, want 0", len(window.Samples))
	}
}

func TestSampler_ToleratesVanishedUnrelatedProcEntries(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	churnDone := make(chan struct{})
	go func() {
		defer close(churnDone)
		for ctx.Err() == nil {
			command := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
			command.Env = append(os.Environ(), helperModeEnv+"=clean")
			if err := command.Run(); err != nil {
				return
			}
		}
	}()

	// When
	for range 100 {
		if _, err := SampleProcess(os.Getpid()); err != nil {
			t.Fatalf("sample during /proc churn: %v", err)
		}
	}
	cancel()
	<-churnDone
}
