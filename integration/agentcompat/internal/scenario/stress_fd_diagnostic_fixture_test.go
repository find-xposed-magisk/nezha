//go:build linux

package scenario

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type fdDiagnosticWindowPair struct {
	baseline fdDiagnosticAgentWindow
	end      fdDiagnosticAgentWindow
}

type stressDiagnosticAgentWindowSpec struct {
	Ordinal           int
	PID               int
	BaselineCount     int
	EndCount          int
	Target            string
	BaselineSampledAt time.Time
	EndSampledAt      time.Time
}

type stressDiagnosticProcessWindowSpec struct {
	PID       int
	Count     int
	Target    string
	SampledAt time.Time
}

type stressDiagnosticDashboardWindowSpec struct {
	PID           int
	BaselineCount int
	EndCount      int
}

func stressDiagnosticAgentWindow(t *testing.T, spec stressDiagnosticAgentWindowSpec) fdDiagnosticWindowPair {
	t.Helper()
	if spec.BaselineSampledAt.IsZero() {
		spec.BaselineSampledAt = time.Unix(int64(spec.Ordinal), 0).UTC()
	}
	if spec.EndSampledAt.IsZero() {
		spec.EndSampledAt = spec.BaselineSampledAt.Add(time.Minute)
	}
	agentOrdinal, err := NewStressAgentOrdinal(spec.Ordinal)
	require.NoError(t, err)
	process, err := NewStressAgentProcess(agentOrdinal, spec.PID)
	require.NoError(t, err)
	identity := agent.ProcessIdentity{Generation: 1, PID: spec.PID}
	return fdDiagnosticWindowPair{
		baseline: fdDiagnosticAgentWindow{Process: process, Identity: identity, Window: stressDiagnosticProcessWindow(stressDiagnosticProcessWindowSpec{PID: spec.PID, Count: spec.BaselineCount, Target: spec.Target, SampledAt: spec.BaselineSampledAt})},
		end:      fdDiagnosticAgentWindow{Process: process, Identity: identity, Window: stressDiagnosticProcessWindow(stressDiagnosticProcessWindowSpec{PID: spec.PID, Count: spec.EndCount, Target: spec.Target, SampledAt: spec.EndSampledAt})},
	}
}

func stressDiagnosticDashboardWindow(t *testing.T, spec stressDiagnosticDashboardWindowSpec) fdDiagnosticWindowPair {
	t.Helper()
	process, err := NewStressDashboardProcess(spec.PID)
	require.NoError(t, err)
	return fdDiagnosticWindowPair{
		baseline: fdDiagnosticAgentWindow{Process: process, Window: stressDiagnosticProcessWindow(stressDiagnosticProcessWindowSpec{PID: spec.PID, Count: spec.BaselineCount, Target: "dashboard"})},
		end:      fdDiagnosticAgentWindow{Process: process, Window: stressDiagnosticProcessWindow(stressDiagnosticProcessWindowSpec{PID: spec.PID, Count: spec.EndCount, Target: "dashboard"})},
	}
}

func stressDiagnosticProcessWindow(spec stressDiagnosticProcessWindowSpec) processharness.Window {
	samples := make([]processharness.Sample, contract.ResourceSampleCount)
	for index := range samples {
		samples[index] = processharness.Sample{PID: spec.PID, NonStdioFDCount: spec.Count, FDObservations: []processharness.FDObservation{{Number: 3, Target: spec.Target}}, SampledAt: spec.SampledAt}
	}
	return processharness.Window{PID: spec.PID, Samples: samples}
}

func stressDiagnosticSample(ordinal int, observations []processharness.FDObservation) fdDiagnosticSample {
	return fdDiagnosticSample{Ordinal: ordinal, FDObservations: observations}
}

func fdDiagnosticTestSampler(_ context.Context, pid int) (processharness.Sample, error) {
	return processharness.Sample{PID: pid, FDObservations: []processharness.FDObservation{{Number: 4, Target: fmt.Sprintf("added-%d", pid)}}, SampledAt: time.Unix(int64(pid), 0).UTC()}, nil
}

func mustStressPRFullProfile(t *testing.T) contract.Profile {
	t.Helper()
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	return profile
}
