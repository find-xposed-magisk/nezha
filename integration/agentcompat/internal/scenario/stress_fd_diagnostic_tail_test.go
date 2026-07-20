//go:build linux && agentcompat

package scenario

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

func TestFDDiagnosticCollector_StartsOverlappingTailsInOrdinalOrder(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstStarts := make(chan int, 2)
	secondStarts := make(chan int, 2)
	completed := make(chan int, 2)
	release := map[int]chan struct{}{101: make(chan struct{}), 102: make(chan struct{})}
	counts := make(map[int]int)
	var countsMu sync.Mutex
	collector := newFDDiagnosticCollector(fdDiagnosticCollectorSpec{
		Enabled:            true,
		SamplerPID:         900,
		TailResultCapacity: 8,
		TailInterval:       0,
		Sample: func(ctx context.Context, pid int) (processharness.Sample, error) {
			countsMu.Lock()
			counts[pid]++
			ordinal := counts[pid]
			countsMu.Unlock()
			if ordinal == 1 {
				firstStarts <- pid
			}
			if ordinal == 2 {
				secondStarts <- pid
				select {
				case <-release[pid]:
				case <-ctx.Done():
					return processharness.Sample{}, ctx.Err()
				}
			}
			if ordinal == 20 {
				completed <- pid
			}
			return processharness.Sample{PID: pid, FDObservations: []processharness.FDObservation{{Number: 3, Target: "tail"}}, SampledAt: time.Unix(int64(ordinal), 0).UTC()}, nil
		},
	})
	pairOne := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "one"})
	pairTwo := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 2, PID: 102, BaselineCount: 8, EndCount: 9, Target: "two"})
	collector.RecordBaseline(pairOne.baseline)
	collector.RecordBaseline(pairTwo.baseline)

	// When
	collector.RecordEnd(ctx, pairOne.end)
	require.Equal(t, 101, <-firstStarts)
	require.Equal(t, 101, <-secondStarts)
	collector.RecordEnd(ctx, pairTwo.end)
	require.Equal(t, 102, <-firstStarts)
	require.Equal(t, 102, <-secondStarts)
	close(release[102])
	require.Equal(t, 102, <-completed)
	close(release[101])
	require.Equal(t, 101, <-completed)
	records := collector.WaitRecords()

	// Then
	require.Len(t, records, 2)
	require.Equal(t, 1, records[0].AgentOrdinal)
	require.Equal(t, 2, records[1].AgentOrdinal)
	for _, record := range records {
		require.Len(t, record.Tail, 20)
		require.Equal(t, 1, record.Tail[0].Ordinal)
		require.Equal(t, 20, record.Tail[19].Ordinal)
		require.Equal(t, time.Unix(1, 0).UTC(), record.Tail[0].ObservedAt)
		require.Equal(t, time.Unix(20, 0).UTC(), record.Tail[19].ObservedAt)
		require.Equal(t, "tail_complete", record.LifecycleStatus)
	}
}

func TestFDDiagnosticCollector_DoesNotSampleWhenDisabled(t *testing.T) {
	// Given
	called := false
	collector := newFDDiagnosticCollector(fdDiagnosticCollectorSpec{
		Enabled: false,
		Sample: func(context.Context, int) (processharness.Sample, error) {
			called = true
			return processharness.Sample{}, nil
		},
	})
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "disabled"})

	// When
	collector.RecordBaseline(pair.baseline)
	collector.RecordEnd(t.Context(), pair.end)
	records := collector.WaitRecords()

	// Then
	require.False(t, called)
	require.Empty(t, records)
}

func TestFDDiagnosticCollector_NilReceiverIsDisabled(t *testing.T) {
	// Given
	var collector *fdDiagnosticCollector
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "nil"})

	// When / Then
	require.NotPanics(t, func() {
		require.False(t, collector.Enabled())
		collector.RecordBaseline(pair.baseline)
		collector.RecordEnd(t.Context(), pair.end)
		require.Empty(t, collector.WaitRecords())
	})
}

func TestFDDiagnosticTail_UsesConfiguredTimerIntervalAfterFirstSample(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstSample := make(chan struct{}, 1)
	sampler := func(context.Context, int) (processharness.Sample, error) {
		firstSample <- struct{}{}
		return processharness.Sample{PID: 101}, nil
	}

	// When
	result := make(chan fdDiagnosticTailResult, 1)
	go func() {
		result <- collectFDDiagnosticTail(fdDiagnosticTailSpec{Context: ctx, PID: 101, Interval: time.Hour, Sample: sampler})
	}()
	<-firstSample
	cancel()
	tail := <-result

	// Then
	require.Len(t, tail.Samples, 1)
	require.ErrorIs(t, tail.Err, context.Canceled)
}

func TestFDDiagnosticTail_DoesNotInvokeSamplerAfterCancellation(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false

	// When
	tail := collectFDDiagnosticTail(fdDiagnosticTailSpec{Context: ctx, PID: 101, Interval: time.Hour, Sample: func(context.Context, int) (processharness.Sample, error) {
		called = true
		return processharness.Sample{}, nil
	}})

	// Then
	require.False(t, called)
	require.ErrorIs(t, tail.Err, context.Canceled)
}
