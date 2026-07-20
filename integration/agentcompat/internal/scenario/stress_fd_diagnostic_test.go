//go:build linux && agentcompat

package scenario

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

func TestFDDiagnosticEnabled_OnlyAcceptsExactOne(t *testing.T) {
	for _, value := range []string{"", "0", "true", "01", " 1", "1 ", "\t1"} {
		require.False(t, fdDiagnosticEnabled(value), "value=%q", value)
	}
	require.True(t, fdDiagnosticEnabled("1"))
}

func TestFDDiagnosticCandidates_ExcludeDashboardAndMatchOrdinals(t *testing.T) {
	collector := newFDDiagnosticCollector(fdDiagnosticCollectorSpec{Enabled: true, TailResultCapacity: 8, Sample: fdDiagnosticTestSampler})
	agentOne := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "one"})
	agentTwo := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 2, PID: 102, BaselineCount: 8, EndCount: 8, Target: "two"})
	dashboard := stressDiagnosticDashboardWindow(t, stressDiagnosticDashboardWindowSpec{PID: 100, BaselineCount: 8, EndCount: 9})
	collector.RecordBaseline(dashboard.baseline)
	collector.RecordBaseline(agentTwo.baseline)
	collector.RecordBaseline(agentOne.baseline)

	collector.RecordEnd(t.Context(), agentTwo.end)
	collector.RecordEnd(t.Context(), dashboard.end)
	collector.RecordEnd(t.Context(), agentOne.end)
	records := collector.WaitRecords()

	require.Len(t, records, 1)
	require.Equal(t, 1, records[0].AgentOrdinal)
	require.Equal(t, 101, records[0].AgentPID)
}

func TestFDDiagnosticCandidates_UseOnlyFinalSampleCounts(t *testing.T) {
	collector := newFDDiagnosticCollector(fdDiagnosticCollectorSpec{Enabled: true, TailResultCapacity: 8, Sample: fdDiagnosticTestSampler})
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 8, Target: "stable"})
	pair.end.Window.Samples[0].NonStdioFDCount = 9
	collector.RecordBaseline(pair.baseline)

	collector.RecordEnd(t.Context(), pair.end)
	require.Empty(t, collector.WaitRecords())

	pair.end.Window.Samples[len(pair.end.Window.Samples)-1].NonStdioFDCount = 9
	collector = newFDDiagnosticCollector(fdDiagnosticCollectorSpec{Enabled: true, TailResultCapacity: 8, Sample: fdDiagnosticTestSampler})
	collector.RecordBaseline(pair.baseline)
	collector.RecordEnd(t.Context(), pair.end)
	require.Len(t, collector.WaitRecords(), 1)
}

func TestFDDiagnosticRecord_ReportsIdentityChangeWithoutTail(t *testing.T) {
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "changed"})
	pair.end.Identity = agent.ProcessIdentity{Generation: 2, PID: 202}
	candidate := fdDiagnosticCandidate{Baseline: pair.baseline, End: pair.end}

	record, tail := newFDDiagnosticRecord(candidate, 999)

	require.Equal(t, "process_identity_changed", record.LifecycleStatus)
	require.False(t, tail)
	require.Empty(t, record.Lifecycle)
}

func TestFDDiagnosticRecord_RejectsStressProcessPIDMismatch(t *testing.T) {
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "process-pid-mismatch"})
	pair.end.Process.PID = 202
	candidate := fdDiagnosticCandidate{Baseline: pair.baseline, End: pair.end}

	record, tail := newFDDiagnosticRecord(candidate, 999)

	require.Equal(t, "process_identity_changed", record.LifecycleStatus)
	require.False(t, tail)
}

func TestFDDiagnosticRecord_RejectsZeroRuntimeGeneration(t *testing.T) {
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "zero-generation"})
	pair.end.Identity.Generation = 0
	candidate := fdDiagnosticCandidate{Baseline: pair.baseline, End: pair.end}

	record, tail := newFDDiagnosticRecord(candidate, 999)

	require.Equal(t, "process_identity_changed", record.LifecycleStatus)
	require.False(t, tail)
}

func TestFDDiagnosticRecord_EncodesStableObservedAtForEveryWindowSample(t *testing.T) {
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "timestamps"})
	baselineTime := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	endTime := baselineTime.Add(time.Minute)
	for index := range pair.baseline.Window.Samples {
		pair.baseline.Window.Samples[index].SampledAt = baselineTime.Add(time.Duration(index) * time.Second)
		pair.end.Window.Samples[index].SampledAt = endTime.Add(time.Duration(index) * time.Second)
	}
	candidate := fdDiagnosticCandidate{Baseline: pair.baseline, End: pair.end}

	record, tail := newFDDiagnosticRecord(candidate, 999)
	encoded, err := json.Marshal(record)

	require.True(t, tail)
	require.NoError(t, err)
	require.Equal(t, baselineTime, record.Baseline.Samples[0].ObservedAt)
	require.Equal(t, endTime, record.End.Samples[0].ObservedAt)
	require.Contains(t, string(encoded), `"observed_at":"2025-01-02T03:04:05Z"`)
}

func TestFDDiagnosticRecord_DiffsFinalObservationsByExactIdentity(t *testing.T) {
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "diff"})
	pair.baseline.Window.Samples[4].FDObservations = []processharness.FDObservation{{Number: 5, Target: "beta"}, {Number: 4, Target: "alpha"}}
	pair.end.Window.Samples[4].FDObservations = []processharness.FDObservation{{Number: 6, Target: "gamma"}, {Number: 5, Target: "beta"}}
	candidate := fdDiagnosticCandidate{Baseline: pair.baseline, End: pair.end}

	record, tail := newFDDiagnosticRecord(candidate, 999)

	require.True(t, tail)
	require.Equal(t, []processharness.FDObservation{{Number: 6, Target: "gamma"}}, record.AddedFinal)
	require.Equal(t, []processharness.FDObservation{{Number: 4, Target: "alpha"}}, record.RemovedFinal)
}

func TestFDDiagnosticLifecycle_ClassifiesEachFinalAddition(t *testing.T) {
	added := []processharness.FDObservation{{Number: 3, Target: "before"}, {Number: 4, Target: "during"}, {Number: 5, Target: "through"}, {Number: 6, Target: "intermittent"}, {Number: 7, Target: "reused"}}
	tail := []fdDiagnosticSample{
		stressDiagnosticSample(1, []processharness.FDObservation{{Number: 4, Target: "during"}, {Number: 5, Target: "through"}, {Number: 6, Target: "intermittent"}, {Number: 7, Target: "reused"}}),
		stressDiagnosticSample(2, []processharness.FDObservation{{Number: 5, Target: "through"}, {Number: 7, Target: "other"}}),
		stressDiagnosticSample(3, []processharness.FDObservation{{Number: 5, Target: "through"}, {Number: 6, Target: "intermittent"}, {Number: 7, Target: "reused"}}),
	}

	lifecycle := classifyFDDiagnosticLifecycle(added, tail)

	require.Equal(t, []fdDiagnosticLifecycle{
		{Observation: processharness.FDObservation{Number: 3, Target: "before"}, Status: "cleared_before_tail"},
		{Observation: processharness.FDObservation{Number: 4, Target: "during"}, Status: "cleared_during_tail"},
		{Observation: processharness.FDObservation{Number: 5, Target: "through"}, Status: "observed_through_tail"},
		{Observation: processharness.FDObservation{Number: 6, Target: "intermittent"}, Status: "intermittent_or_reused"},
		{Observation: processharness.FDObservation{Number: 7, Target: "reused"}, Status: "intermittent_or_reused", NumberReused: true},
	}, lifecycle)
}

func TestFDDiagnosticFields_DoNotChangeStressEvaluationOrEvidenceJSON(t *testing.T) {
	resource := stressDashboardResourceFixture(100)
	beforeEvaluation, err := EvaluateStressResource(resource)
	require.NoError(t, err)
	for sampleIndex := range resource.Baseline.Samples {
		resource.Baseline.Samples[sampleIndex].FDObservations = []processharness.FDObservation{{Number: 9, Target: "diagnostic"}}
		resource.End.Samples[sampleIndex].FDObservations = []processharness.FDObservation{{Number: 9, Target: "diagnostic"}}
	}
	afterEvaluation, err := EvaluateStressResource(resource)
	require.NoError(t, err)
	profile := mustStressPRFullProfile(t)
	evidence := validStressEvidence(t, profile)
	beforeEvidence, err := json.Marshal(evidence)
	require.NoError(t, err)
	for iteration := range evidence.Iterations {
		for resourceIndex := range evidence.Iterations[iteration].Resources {
			for sampleIndex := range evidence.Iterations[iteration].Resources[resourceIndex].Baseline.Samples {
				evidence.Iterations[iteration].Resources[resourceIndex].Baseline.Samples[sampleIndex].FDObservations = []processharness.FDObservation{{Number: 9, Target: "diagnostic"}}
				evidence.Iterations[iteration].Resources[resourceIndex].End.Samples[sampleIndex].FDObservations = []processharness.FDObservation{{Number: 9, Target: "diagnostic"}}
			}
		}
	}
	afterEvidence, err := json.Marshal(evidence)
	require.NoError(t, err)

	require.Equal(t, beforeEvaluation, afterEvaluation)
	require.Equal(t, string(beforeEvidence), string(afterEvidence))
}
