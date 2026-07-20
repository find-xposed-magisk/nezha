//go:build linux && agentcompat

package scenario

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

const fdDiagnosticTailSampleCount = 20

type fdDiagnosticSampleFunc func(context.Context, int) (processharness.Sample, error)

type fdDiagnosticLogger interface {
	Logf(string, ...any)
}

type fdDiagnosticCollectorSpec struct {
	Enabled            bool
	SamplerPID         int
	TailResultCapacity int
	TailInterval       time.Duration
	Sample             fdDiagnosticSampleFunc
}

type fdDiagnosticTailResult struct {
	AgentOrdinal int
	Samples      []fdDiagnosticSample
	Err          error
}

type fdDiagnosticTailSpec struct {
	Context             context.Context
	PID                 int
	Interval            time.Duration
	Sample              fdDiagnosticSampleFunc
	FirstSampleComplete chan<- struct{}
}

type fdDiagnosticCollector struct {
	enabled   bool
	samplerID int
	interval  time.Duration
	sample    fdDiagnosticSampleFunc
	baseline  map[int]fdDiagnosticAgentWindow
	records   map[int]fdDiagnosticRecord
	results   chan fdDiagnosticTailResult
	started   int
	waitOnce  sync.Once
	logOnce   sync.Once
	completed []fdDiagnosticRecord
}

func newFDDiagnosticCollector(spec fdDiagnosticCollectorSpec) *fdDiagnosticCollector {
	capacity := spec.TailResultCapacity
	if capacity < contract.PRFullAgentCount {
		capacity = contract.PRFullAgentCount
	}
	return &fdDiagnosticCollector{
		enabled:   spec.Enabled,
		samplerID: spec.SamplerPID,
		interval:  spec.TailInterval,
		sample:    spec.Sample,
		baseline:  make(map[int]fdDiagnosticAgentWindow),
		records:   make(map[int]fdDiagnosticRecord),
		results:   make(chan fdDiagnosticTailResult, capacity),
	}
}

func newRealFDDiagnosticCollector(enabled bool) *fdDiagnosticCollector {
	return newFDDiagnosticCollector(fdDiagnosticCollectorSpec{
		Enabled:            enabled,
		SamplerPID:         os.Getpid(),
		TailResultCapacity: contract.PRFullAgentCount,
		TailInterval:       contract.ResourceSampleInterval,
		Sample: func(_ context.Context, pid int) (processharness.Sample, error) {
			return processharness.SampleProcessWithFDObservations(pid)
		},
	})
}

func (collector *fdDiagnosticCollector) Enabled() bool { return collector != nil && collector.enabled }

func (collector *fdDiagnosticCollector) RecordBaseline(window fdDiagnosticAgentWindow) {
	if collector.Enabled() && window.Process.Kind == StressProcessAgent {
		collector.baseline[window.Process.Agent.Int()] = window
	}
}

func (collector *fdDiagnosticCollector) RecordEnd(ctx context.Context, window fdDiagnosticAgentWindow) {
	if !collector.Enabled() || window.Process.Kind != StressProcessAgent {
		return
	}
	baseline, exists := collector.baseline[window.Process.Agent.Int()]
	if !exists || fdDiagnosticFinalCount(baseline.Window) == fdDiagnosticFinalCount(window.Window) {
		return
	}
	record, startTail := newFDDiagnosticRecord(fdDiagnosticCandidate{Baseline: baseline, End: window}, collector.samplerID)
	collector.records[record.AgentOrdinal] = record
	if !startTail {
		return
	}
	collector.started++
	firstSampleComplete := make(chan struct{})
	tailSpec := fdDiagnosticTailSpec{Context: ctx, PID: record.AgentPID, Interval: collector.interval, Sample: collector.sample, FirstSampleComplete: firstSampleComplete}
	go func(ordinal int) {
		result := collectFDDiagnosticTail(tailSpec)
		result.AgentOrdinal = ordinal
		collector.results <- result
	}(record.AgentOrdinal)
	select {
	case <-firstSampleComplete:
	case <-ctx.Done():
	}
}

func (collector *fdDiagnosticCollector) WaitRecords() []fdDiagnosticRecord {
	if !collector.Enabled() {
		return nil
	}
	collector.waitOnce.Do(func() {
		for range collector.started {
			result := <-collector.results
			record := collector.records[result.AgentOrdinal]
			record.Tail = result.Samples
			if result.Err != nil {
				record.DiagnosticError = result.Err.Error()
				record.LifecycleStatus = "tail_error"
			} else {
				record.Lifecycle = classifyFDDiagnosticLifecycle(record.AddedFinal, record.Tail)
				record.LifecycleStatus = "tail_complete"
			}
			collector.records[result.AgentOrdinal] = record
		}
		collector.completed = make([]fdDiagnosticRecord, 0, len(collector.records))
		for _, record := range collector.records {
			collector.completed = append(collector.completed, record)
		}
		sort.Slice(collector.completed, func(left, right int) bool {
			return collector.completed[left].AgentOrdinal < collector.completed[right].AgentOrdinal
		})
	})
	return append([]fdDiagnosticRecord(nil), collector.completed...)
}

// The real collector is directly logger-injectable so tests exercise candidate selection and logging without shadow copies.
func (collector *fdDiagnosticCollector) WaitAndLog(logger fdDiagnosticLogger) {
	if !collector.Enabled() {
		return
	}
	collector.logOnce.Do(func() {
		for _, record := range collector.WaitRecords() {
			encoded, err := json.Marshal(record)
			if err != nil {
				logger.Logf("agentcompat_fd_diagnostic={\"diagnostic_error\":%q}", err.Error())
				continue
			}
			logger.Logf("agentcompat_fd_diagnostic=%s", encoded)
		}
	})
}

func collectFDDiagnosticTail(spec fdDiagnosticTailSpec) fdDiagnosticTailResult {
	result := fdDiagnosticTailResult{Samples: make([]fdDiagnosticSample, 0, fdDiagnosticTailSampleCount)}
	signalFirstSampleComplete := func() {
		if spec.FirstSampleComplete != nil {
			close(spec.FirstSampleComplete)
			spec.FirstSampleComplete = nil
		}
	}
	for index := 0; index < fdDiagnosticTailSampleCount; index++ {
		if index > 0 {
			timer := time.NewTimer(spec.Interval)
			select {
			case <-timer.C:
			case <-spec.Context.Done():
				timer.Stop()
				result.Err = spec.Context.Err()
				return result
			}
		}
		if err := spec.Context.Err(); err != nil {
			signalFirstSampleComplete()
			result.Err = err
			return result
		}
		processSample, err := spec.Sample(spec.Context, spec.PID)
		if err != nil {
			signalFirstSampleComplete()
			result.Err = err
			return result
		}
		result.Samples = append(result.Samples, fdDiagnosticSampleFromProcess(index+1, processSample))
		signalFirstSampleComplete()
	}
	return result
}

func fdDiagnosticSampleFromProcess(ordinal int, sample processharness.Sample) fdDiagnosticSample {
	return fdDiagnosticSample{Ordinal: ordinal, PID: sample.PID, RSSBytes: sample.RSSBytes, DescendantPIDs: append([]int(nil), sample.DescendantPIDs...), DescendantCount: sample.DescendantCount, NonStdioFDCount: sample.NonStdioFDCount, TCPListenerCount: sample.TCPListenerCount, TCP6ListenerCount: sample.TCP6ListenerCount, FDObservations: fdDiagnosticSortedObservations(sample.FDObservations), ObservedAt: sample.SampledAt}
}
