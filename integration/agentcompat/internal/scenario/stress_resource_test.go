//go:build linux

package scenario

import (
	"testing"

	"github.com/stretchr/testify/require"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

func TestStress_RejectsLeakedFD(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	for index := range input.End.Samples {
		input.End.Samples[index].NonStdioFDCount++
	}

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsSingleSampleFDTransient(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.End.Samples[4].NonStdioFDCount++

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_AcceptsRecoveredBaselineFDTransient(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.Baseline.Samples[1].NonStdioFDCount = 9

	evaluation, err := EvaluateStressResource(input)

	require.NoError(t, err)
	require.Equal(t, 3, evaluation.Baseline.NonStdioFDs)
	require.Equal(t, 3, evaluation.End.NonStdioFDs)
}

func TestStress_RejectsSingleSampleDescendantTransient(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.End.Samples[4].DescendantCount++
	_, err := EvaluateStressResource(input)
	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsSingleSampleTCPTransient(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.End.Samples[4].TCPListenerCount++
	_, err := EvaluateStressResource(input)
	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsSingleSampleTCP6Transient(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.End.Samples[4].TCP6ListenerCount++
	_, err := EvaluateStressResource(input)
	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsDecreasedDescendants(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	for index := range input.End.Samples {
		input.End.Samples[index].DescendantCount = 0
		input.Baseline.Samples[index].DescendantCount = 1
	}

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsDecreasedFDs(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	for index := range input.End.Samples {
		input.End.Samples[index].NonStdioFDCount = 2
		input.Baseline.Samples[index].NonStdioFDCount = 3
	}

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsDecreasedTCPListeners(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	for index := range input.End.Samples {
		input.End.Samples[index].TCPListenerCount = 0
		input.Baseline.Samples[index].TCPListenerCount = 1
	}

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsDecreasedTCP6Listeners(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	for index := range input.End.Samples {
		input.End.Samples[index].TCP6ListenerCount = 0
		input.Baseline.Samples[index].TCP6ListenerCount = 1
	}

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressResourceDrift)
}

func TestStress_RejectsDashboardRSSOverLimit(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.End.Samples[4].RSSBytes = 100 + 67108865

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressRSSLimit)
}

func TestStress_RejectsAgentRSSOverLimit(t *testing.T) {
	agent, err := NewStressAgentOrdinal(4)
	require.NoError(t, err)
	identity, err := NewStressAgentProcess(agent, 404)
	require.NoError(t, err)
	input := StressProcessWindows{Process: identity, Baseline: stressWindow(404, 100), End: stressWindow(404, 100+33554433)}

	_, err = EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressRSSLimit)
}

func TestStress_RejectsVanishedPID(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.End.Samples[2].PID = 0

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressProcessWindow)
}

func TestStress_RejectsIncompleteSampleWindow(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.End.Samples = input.End.Samples[:4]

	_, err := EvaluateStressResource(input)

	require.ErrorIs(t, err, ErrStressProcessWindow)
}

func TestStress_UsesFinalCountSampleButWindowRSSMaximum(t *testing.T) {
	input := stressDashboardResourceFixture(100)
	input.Baseline.Samples[0].NonStdioFDCount = 4
	input.End.Samples[0].NonStdioFDCount = 4
	input.End.Samples[4].RSSBytes = 110

	evaluation, err := EvaluateStressResource(input)

	require.NoError(t, err)
	require.Equal(t, uint64(10), evaluation.RSSDeltaBytes)
}

func stressDashboardResourceFixture(rss uint64) StressProcessWindows {
	identity, err := NewStressDashboardProcess(101)
	if err != nil {
		panic(err)
	}
	return StressProcessWindows{Process: identity, Baseline: stressWindow(101, rss), End: stressWindow(101, rss)}
}

func stressWindow(pid int, rss uint64) processharness.Window {
	samples := make([]processharness.Sample, 5)
	for index := range samples {
		samples[index] = processharness.Sample{PID: pid, RSSBytes: rss, NonStdioFDCount: 3, TCPListenerCount: 1}
	}
	return processharness.Window{PID: pid, Samples: samples}
}
