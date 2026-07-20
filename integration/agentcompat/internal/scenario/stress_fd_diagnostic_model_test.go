//go:build linux

package scenario

import (
	"sort"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type fdDiagnosticAgentWindow struct {
	Process  StressProcessIdentity
	Identity agent.ProcessIdentity
	Window   processharness.Window
}

type fdDiagnosticCandidate struct {
	Baseline fdDiagnosticAgentWindow
	End      fdDiagnosticAgentWindow
}

type fdDiagnosticSample struct {
	Ordinal           int                            `json:"sample_ordinal"`
	PID               int                            `json:"pid"`
	RSSBytes          uint64                         `json:"rss_bytes"`
	DescendantPIDs    []int                          `json:"descendant_pids"`
	DescendantCount   int                            `json:"descendant_count"`
	NonStdioFDCount   int                            `json:"non_stdio_fd_count"`
	TCPListenerCount  int                            `json:"tcp_listener_count"`
	TCP6ListenerCount int                            `json:"tcp6_listener_count"`
	FDObservations    []processharness.FDObservation `json:"fd_observations"`
	ObservedAt        time.Time                      `json:"observed_at"`
}

type fdDiagnosticWindow struct {
	PID     int                  `json:"pid"`
	Samples []fdDiagnosticSample `json:"samples"`
}

type fdDiagnosticLifecycle struct {
	Observation  processharness.FDObservation `json:"observation"`
	Status       string                       `json:"status"`
	NumberReused bool                         `json:"number_reused,omitempty"`
}

type fdDiagnosticRecord struct {
	SamplerPID         int                            `json:"sampler_pid"`
	AgentOrdinal       int                            `json:"agent_ordinal"`
	AgentPID           int                            `json:"agent_pid"`
	BaselineGeneration uint64                         `json:"baseline_generation"`
	BaselinePID        int                            `json:"baseline_pid"`
	EndPID             int                            `json:"end_pid"`
	EndGeneration      uint64                         `json:"end_generation"`
	Baseline           *fdDiagnosticWindow            `json:"baseline"`
	End                *fdDiagnosticWindow            `json:"end"`
	Tail               []fdDiagnosticSample           `json:"tail"`
	AddedFinal         []processharness.FDObservation `json:"added_final"`
	RemovedFinal       []processharness.FDObservation `json:"removed_final"`
	Lifecycle          []fdDiagnosticLifecycle        `json:"lifecycle"`
	LifecycleStatus    string                         `json:"lifecycle_status"`
	DiagnosticError    string                         `json:"diagnostic_error,omitempty"`
}

func fdDiagnosticEnabled(value string) bool { return value == "1" }

func newFDDiagnosticRecord(candidate fdDiagnosticCandidate, samplerPID int) (fdDiagnosticRecord, bool) {
	record := fdDiagnosticRecord{
		SamplerPID:         samplerPID,
		AgentOrdinal:       candidate.Baseline.Process.Agent.Int(),
		AgentPID:           candidate.Baseline.Identity.PID,
		BaselineGeneration: candidate.Baseline.Identity.Generation,
		BaselinePID:        candidate.Baseline.Identity.PID,
		EndPID:             candidate.End.Identity.PID,
		EndGeneration:      candidate.End.Identity.Generation,
		Baseline:           fdDiagnosticWindowFromProcess(candidate.Baseline.Window),
		End:                fdDiagnosticWindowFromProcess(candidate.End.Window),
	}
	if !fdDiagnosticIdentityMatches(candidate) {
		record.LifecycleStatus = "process_identity_changed"
		return record, false
	}
	baselineFinal := fdDiagnosticFinalObservations(candidate.Baseline.Window)
	endFinal := fdDiagnosticFinalObservations(candidate.End.Window)
	record.AddedFinal = fdDiagnosticDifference(endFinal, baselineFinal)
	record.RemovedFinal = fdDiagnosticDifference(baselineFinal, endFinal)
	record.LifecycleStatus = "tail_pending"
	return record, true
}

func classifyFDDiagnosticLifecycle(added []processharness.FDObservation, tail []fdDiagnosticSample) []fdDiagnosticLifecycle {
	result := make([]fdDiagnosticLifecycle, 0, len(added))
	for _, observation := range added {
		present := make([]bool, len(tail))
		reused := false
		for index, sample := range tail {
			for _, tailObservation := range sample.FDObservations {
				if tailObservation.Number == observation.Number && tailObservation.Target != observation.Target {
					reused = true
				}
				if tailObservation == observation {
					present[index] = true
				}
			}
		}
		status := "intermittent_or_reused"
		switch {
		case reused:
			status = "intermittent_or_reused"
		case !fdDiagnosticAnyPresent(present):
			status = "cleared_before_tail"
		case fdDiagnosticAllPresent(present):
			status = "observed_through_tail"
		case fdDiagnosticPrefixPresent(present):
			status = "cleared_during_tail"
		}
		result = append(result, fdDiagnosticLifecycle{Observation: observation, Status: status, NumberReused: reused})
	}
	return result
}

func fdDiagnosticFinalCount(window processharness.Window) int {
	if len(window.Samples) == 0 {
		return 0
	}
	return window.Samples[len(window.Samples)-1].NonStdioFDCount
}

func fdDiagnosticIdentityMatches(candidate fdDiagnosticCandidate) bool {
	baseline := candidate.Baseline
	end := candidate.End
	return baseline.Process.PID > 0 && baseline.Process.PID == baseline.Identity.PID && baseline.Identity.PID == baseline.Window.PID && end.Process.PID > 0 && end.Process.PID == end.Identity.PID && end.Identity.PID == end.Window.PID && baseline.Process.PID == end.Process.PID && baseline.Identity.Generation != 0 && baseline.Identity.Generation == end.Identity.Generation
}

func fdDiagnosticWindowFromProcess(window processharness.Window) *fdDiagnosticWindow {
	result := &fdDiagnosticWindow{PID: window.PID, Samples: make([]fdDiagnosticSample, 0, len(window.Samples))}
	for index, sample := range window.Samples {
		result.Samples = append(result.Samples, fdDiagnosticSample{Ordinal: index + 1, PID: sample.PID, RSSBytes: sample.RSSBytes, DescendantPIDs: append([]int(nil), sample.DescendantPIDs...), DescendantCount: sample.DescendantCount, NonStdioFDCount: sample.NonStdioFDCount, TCPListenerCount: sample.TCPListenerCount, TCP6ListenerCount: sample.TCP6ListenerCount, FDObservations: fdDiagnosticSortedObservations(sample.FDObservations), ObservedAt: sample.SampledAt})
	}
	return result
}

func fdDiagnosticFinalObservations(window processharness.Window) []processharness.FDObservation {
	if len(window.Samples) == 0 {
		return nil
	}
	return fdDiagnosticSortedObservations(window.Samples[len(window.Samples)-1].FDObservations)
}

func fdDiagnosticDifference(left, right []processharness.FDObservation) []processharness.FDObservation {
	rightSet := make(map[processharness.FDObservation]struct{}, len(right))
	for _, observation := range right {
		rightSet[observation] = struct{}{}
	}
	result := make([]processharness.FDObservation, 0)
	for _, observation := range left {
		if _, exists := rightSet[observation]; !exists {
			result = append(result, observation)
		}
	}
	return fdDiagnosticSortedObservations(result)
}

func fdDiagnosticSortedObservations(input []processharness.FDObservation) []processharness.FDObservation {
	result := append([]processharness.FDObservation(nil), input...)
	sort.Slice(result, func(left, right int) bool {
		return result[left].Number < result[right].Number || (result[left].Number == result[right].Number && result[left].Target < result[right].Target)
	})
	return result
}

func fdDiagnosticAllPresent(present []bool) bool {
	for _, value := range present {
		if !value {
			return false
		}
	}
	return true
}

func fdDiagnosticAnyPresent(present []bool) bool {
	for _, value := range present {
		if value {
			return true
		}
	}
	return false
}

func fdDiagnosticPrefixPresent(present []bool) bool {
	cleared := false
	for _, value := range present {
		if !value {
			cleared = true
		} else if cleared {
			return false
		}
	}
	return cleared
}
