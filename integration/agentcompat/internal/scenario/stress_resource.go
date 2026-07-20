//go:build linux

package scenario

import (
	"errors"
	"fmt"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

var (
	ErrStressProcessWindow = errors.New("stress process window is invalid")
	ErrStressResourceDrift = errors.New("stress process resource count drifted")
	ErrStressRSSLimit      = errors.New("stress process RSS limit exceeded")
)

type StressProcessWindows struct {
	Process  StressProcessIdentity `json:"process"`
	Baseline processharness.Window `json:"baseline"`
	End      processharness.Window `json:"end"`
}

type StressResourceMaxima struct {
	RSSBytes      uint64 `json:"rss_bytes"`
	Descendants   int    `json:"descendants"`
	NonStdioFDs   int    `json:"non_stdio_fds"`
	TCPListeners  int    `json:"tcp_listeners"`
	TCP6Listeners int    `json:"tcp6_listeners"`
}

type StressResourceEvaluation struct {
	Process       StressProcessIdentity `json:"process"`
	Baseline      StressResourceMaxima  `json:"baseline"`
	End           StressResourceMaxima  `json:"end"`
	RSSDeltaBytes uint64                `json:"rss_delta_bytes"`
	RSSLimitBytes uint64                `json:"rss_limit_bytes"`
}

func EvaluateStressResource(input StressProcessWindows) (StressResourceEvaluation, error) {
	baseline, err := stressWindowMaxima(input.Process, input.Baseline)
	if err != nil {
		return StressResourceEvaluation{}, err
	}
	end, err := stressWindowMaxima(input.Process, input.End)
	if err != nil {
		return StressResourceEvaluation{}, err
	}
	budget := contract.DefaultResourceBudget()
	if end.Descendants != baseline.Descendants || end.NonStdioFDs != baseline.NonStdioFDs || end.TCPListeners != baseline.TCPListeners || end.TCP6Listeners != baseline.TCP6Listeners {
		return StressResourceEvaluation{}, fmt.Errorf("process=%s baseline=%+v end=%+v baseline_samples=%+v end_samples=%+v: %w", input.Process.key(), baseline, end, resourceCountSamples(input.Baseline), resourceCountSamples(input.End), ErrStressResourceDrift)
	}
	limit, err := stressRSSLimit(input.Process, budget)
	if err != nil {
		return StressResourceEvaluation{}, err
	}
	delta := uint64(0)
	if end.RSSBytes > baseline.RSSBytes {
		delta = end.RSSBytes - baseline.RSSBytes
	}
	if delta > limit {
		return StressResourceEvaluation{}, fmt.Errorf("process=%s RSS delta=%d limit=%d: %w", input.Process.key(), delta, limit, ErrStressRSSLimit)
	}
	return StressResourceEvaluation{Process: input.Process, Baseline: baseline, End: end, RSSDeltaBytes: delta, RSSLimitBytes: limit}, nil
}

func resourceCountSamples(window processharness.Window) []string {
	result := make([]string, 0, len(window.Samples))
	for _, sample := range window.Samples {
		result = append(result, fmt.Sprintf("descendants=%d fd=%d tcp=%d tcp6=%d", sample.DescendantCount, sample.NonStdioFDCount, sample.TCPListenerCount, sample.TCP6ListenerCount))
	}
	return result
}

func stressWindowMaxima(process StressProcessIdentity, window processharness.Window) (StressResourceMaxima, error) {
	if process.PID < 1 || window.PID != process.PID || len(window.Samples) != contract.ResourceSampleCount {
		return StressResourceMaxima{}, fmt.Errorf("process=%s window PID=%d samples=%d: %w", process.key(), window.PID, len(window.Samples), ErrStressProcessWindow)
	}
	maxima := StressResourceMaxima{}
	for _, sample := range window.Samples {
		if sample.PID != process.PID || sample.PID < 1 {
			return StressResourceMaxima{}, fmt.Errorf("process=%s sample PID=%d: %w", process.key(), sample.PID, ErrStressProcessWindow)
		}
		maxima.RSSBytes = max(maxima.RSSBytes, sample.RSSBytes)
		// Count drift compares each window's terminal state. Using a baseline high-water
		// mark turns legitimate short-lived work that has already drained into a leak.
		maxima.Descendants = sample.DescendantCount
		maxima.NonStdioFDs = sample.NonStdioFDCount
		maxima.TCPListeners = sample.TCPListenerCount
		maxima.TCP6Listeners = sample.TCP6ListenerCount
	}
	return maxima, nil
}

func stressRSSLimit(process StressProcessIdentity, budget contract.ResourceBudget) (uint64, error) {
	switch process.Kind {
	case StressProcessDashboard:
		return budget.DashboardRSSDeltaBytes(), nil
	case StressProcessAgent:
		if process.Agent.Int() < 1 {
			return 0, fmt.Errorf("process=%s: %w", process.key(), ErrStressIdentity)
		}
		return budget.AgentRSSDeltaBytes(), nil
	default:
		return 0, fmt.Errorf("process kind=%q: %w", process.Kind, ErrStressIdentity)
	}
}
