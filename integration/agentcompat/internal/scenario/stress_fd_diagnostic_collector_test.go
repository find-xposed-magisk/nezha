//go:build linux && agentcompat

package scenario

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

func TestFDDiagnosticCollector_UsesOnlyFinalSampleCountThroughCollectorWiring(t *testing.T) {
	// Given
	collector := newFDDiagnosticCollector(fdDiagnosticCollectorSpec{Enabled: true, TailResultCapacity: 8, Sample: fdDiagnosticTestSampler})
	pair := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 8, Target: "stable"})
	pair.end.Window.Samples[0].NonStdioFDCount = 9
	collector.RecordBaseline(pair.baseline)

	// When
	collector.RecordEnd(t.Context(), pair.end)
	records := collector.WaitRecords()

	// Then
	require.Empty(t, records)
}

func TestFDDiagnosticCollector_OrdersAndLogsStableCompleteRecords(t *testing.T) {
	// Given
	collector := newFDDiagnosticCollector(fdDiagnosticCollectorSpec{Enabled: true, SamplerPID: 900, TailResultCapacity: 8, Sample: fdDiagnosticTestSampler})
	agentOne := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 1, PID: 101, BaselineCount: 8, EndCount: 9, Target: "one"})
	agentTwo := stressDiagnosticAgentWindow(t, stressDiagnosticAgentWindowSpec{Ordinal: 2, PID: 102, BaselineCount: 8, EndCount: 9, Target: "two"})
	fdDiagnosticSetFinalObservations(&agentOne, 101)
	fdDiagnosticSetFinalObservations(&agentTwo, 102)
	collector.RecordBaseline(agentTwo.baseline)
	collector.RecordBaseline(agentOne.baseline)

	// When
	collector.RecordEnd(t.Context(), agentTwo.end)
	collector.RecordEnd(t.Context(), agentOne.end)
	records := collector.WaitRecords()
	logs := make([]string, 0, len(records))
	logger := &fdDiagnosticMemoryLogger{lines: &logs}
	collector.WaitAndLog(logger)
	collector.WaitAndLog(logger)

	// Then
	require.Len(t, records, 2)
	require.Equal(t, []int{1, 2}, []int{records[0].AgentOrdinal, records[1].AgentOrdinal})
	require.Len(t, logs, 2)
	for index, line := range logs {
		require.True(t, strings.HasPrefix(line, "agentcompat_fd_diagnostic="))
		var record fdDiagnosticRecord
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "agentcompat_fd_diagnostic=")), &record))
		require.Equal(t, index+1, record.AgentOrdinal)
		require.NotEmpty(t, record.Baseline.Samples[0].FDObservations)
		require.NotEmpty(t, record.End.Samples[0].FDObservations)
		require.NotEmpty(t, record.Tail[0].FDObservations)
		require.False(t, record.Baseline.Samples[0].ObservedAt.IsZero())
		require.False(t, record.End.Samples[0].ObservedAt.IsZero())
		require.False(t, record.Tail[0].ObservedAt.IsZero())
		require.Equal(t, "observed_through_tail", record.Lifecycle[0].Status)
	}
}

func fdDiagnosticSetFinalObservations(pair *fdDiagnosticWindowPair, pid int) {
	lastSample := len(pair.baseline.Window.Samples) - 1
	pair.baseline.Window.Samples[lastSample].FDObservations = []processharness.FDObservation{{Number: 3, Target: fmt.Sprintf("baseline-%d", pid)}}
	pair.end.Window.Samples[lastSample].FDObservations = []processharness.FDObservation{{Number: 4, Target: fmt.Sprintf("added-%d", pid)}}
}

type fdDiagnosticMemoryLogger struct {
	lines *[]string
}

func (logger *fdDiagnosticMemoryLogger) Logf(format string, arguments ...any) {
	*logger.lines = append(*logger.lines, fmt.Sprintf(format, arguments...))
}
