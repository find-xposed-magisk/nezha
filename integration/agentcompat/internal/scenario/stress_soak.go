//go:build linux

package scenario

import (
	"errors"
	"fmt"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

var ErrStressSoakTrend = errors.New("stress soak RSS increases strictly across all iterations")

type StressRSSSeries struct {
	Process     StressProcessIdentity `json:"process"`
	EndRSSBytes [3]uint64             `json:"end_rss_bytes"`
}

type StressSoakTrendEvidence struct {
	Series []StressRSSSeries `json:"series"`
}

func ValidateStressSoakTrend(evidence StressSoakTrendEvidence) error {
	if len(evidence.Series) == 0 {
		return errors.New("stress soak RSS series are empty")
	}
	seen := make(map[string]struct{}, len(evidence.Series))
	for _, series := range evidence.Series {
		key := series.Process.key()
		if series.Process.PID < 1 || key == ":0" || (series.Process.Kind == StressProcessAgent && series.Process.Agent.Int() < 1) {
			return fmt.Errorf("process=%s: %w", key, ErrStressIdentity)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate process=%s: %w", key, ErrStressIdentity)
		}
		seen[key] = struct{}{}
		values := series.EndRSSBytes
		if values[0] < values[1] && values[1] < values[2] {
			return fmt.Errorf("process=%s RSS=%v: %w", key, values, ErrStressSoakTrend)
		}
	}
	return nil
}

func ValidateStressSoakTrendForProfile(profile contract.Profile, evidence StressSoakTrendEvidence) error {
	want := profile.AgentCount() + 1
	if len(evidence.Series) != want {
		return fmt.Errorf("soak series=%d want=%d: %w", len(evidence.Series), want, ErrStressIdentity)
	}
	seenDashboard := 0
	seenAgents := make(map[int]struct{}, profile.AgentCount())
	for _, series := range evidence.Series {
		if err := validateStressSoakSeries(series); err != nil {
			return err
		}
		switch series.Process.Kind {
		case StressProcessDashboard:
			seenDashboard++
		case StressProcessAgent:
			ordinal := series.Process.Agent.Int()
			if ordinal < 1 || ordinal > profile.AgentCount() {
				return fmt.Errorf("unknown agent ordinal=%d: %w", ordinal, ErrStressIdentity)
			}
			if _, duplicate := seenAgents[ordinal]; duplicate {
				return fmt.Errorf("duplicate agent ordinal=%d: %w", ordinal, ErrStressIdentity)
			}
			seenAgents[ordinal] = struct{}{}
		default:
			return fmt.Errorf("unknown process kind=%q: %w", series.Process.Kind, ErrStressIdentity)
		}
	}
	if seenDashboard != 1 || len(seenAgents) != profile.AgentCount() {
		return fmt.Errorf("dashboard=%d agents=%d: %w", seenDashboard, len(seenAgents), ErrStressIdentity)
	}
	return nil
}

func validateStressSoakSeries(series StressRSSSeries) error {
	key := series.Process.key()
	if series.Process.PID < 1 || key == ":0" || (series.Process.Kind == StressProcessAgent && series.Process.Agent.Int() < 1) {
		return fmt.Errorf("process=%s: %w", key, ErrStressIdentity)
	}
	values := series.EndRSSBytes
	if values[0] < values[1] && values[1] < values[2] {
		return fmt.Errorf("process=%s RSS=%v: %w", key, values, ErrStressSoakTrend)
	}
	return nil
}
