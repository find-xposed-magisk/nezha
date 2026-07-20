//go:build linux

package process

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
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

func TestSampleProcess_DoesNotCaptureFDObservationsByDefault(t *testing.T) {
	// Given / When
	sample, err := SampleProcess(os.Getpid())

	// Then
	requireNoError(t, err)
	if sample.FDObservations != nil {
		t.Fatalf("SampleProcess FD observations = %v, want nil", sample.FDObservations)
	}
}

func TestSampleWindow_DoesNotCaptureFDObservationsByDefault(t *testing.T) {
	// Given / When
	window, err := SampleWindow(t.Context(), WindowSpec{PID: os.Getpid(), Interval: time.Millisecond})

	// Then
	requireNoError(t, err)
	for index, windowSample := range window.Samples {
		if windowSample.FDObservations != nil {
			t.Fatalf("sample %d FD observations = %v, want nil", index+1, windowSample.FDObservations)
		}
	}
}

func TestSampleWindow_CapturesFDObservationsAcrossKnownFileLifecycle(t *testing.T) {
	// Given
	file, err := os.Open("/proc/self/status")
	requireNoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	descriptor := int(file.Fd())
	target, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(descriptor))
	requireNoError(t, err)
	observed := 0

	// When
	window, err := SampleWindow(t.Context(), WindowSpec{
		PID:                   os.Getpid(),
		Interval:              time.Millisecond,
		CaptureFDObservations: true,
		ObserveSample: func(_ context.Context, sample Sample) error {
			if observed == 0 {
				if err := file.Close(); err != nil {
					return err
				}
			}
			observed++
			return nil
		},
	})

	// Then
	requireNoError(t, err)
	if len(window.Samples) != 5 || observed != 5 {
		t.Fatalf("samples = %d, observed = %d, want 5", len(window.Samples), observed)
	}
	firstSampleContainsOpenedFile := false
	for _, observation := range window.Samples[0].FDObservations {
		if observation.Number == descriptor && observation.Target == target {
			firstSampleContainsOpenedFile = true
			break
		}
	}
	if !firstSampleContainsOpenedFile {
		t.Fatalf("sample 1 observations = %v, want FD %d target %q", window.Samples[0].FDObservations, descriptor, target)
	}
	for index, sample := range window.Samples {
		if len(sample.FDObservations) != sample.NonStdioFDCount {
			t.Fatalf("sample %d observations = %d, non-stdio FDs = %d", index+1, len(sample.FDObservations), sample.NonStdioFDCount)
		}
		if !sort.SliceIsSorted(sample.FDObservations, func(left, right int) bool {
			leftObservation := sample.FDObservations[left]
			rightObservation := sample.FDObservations[right]
			return leftObservation.Number < rightObservation.Number || (leftObservation.Number == rightObservation.Number && leftObservation.Target < rightObservation.Target)
		}) {
			t.Fatalf("sample %d FD observations are not sorted: %v", index+1, sample.FDObservations)
		}
		if index > 0 {
			for _, observation := range sample.FDObservations {
				if observation.Number == descriptor {
					t.Fatalf("sample %d observations unexpectedly retain closed FD %d: %v", index+1, descriptor, sample.FDObservations)
				}
			}
		}
	}
	encoded, err := json.Marshal(window.Samples[0])
	requireNoError(t, err)
	var evidence map[string]json.RawMessage
	requireNoError(t, json.Unmarshal(encoded, &evidence))
	if _, exists := evidence["fd_observations"]; exists {
		t.Fatalf("JSON evidence includes FD observations: %s", encoded)
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
