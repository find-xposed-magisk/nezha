package model

import (
	"math"
	"testing"
	"time"
)

func TestRuleSnapshotIgnoresEmptyMetricSamples(t *testing.T) {
	tests := []struct {
		name      string
		ruleType  string
		hostState *HostState
	}{
		{
			name:      "GPU list is empty",
			ruleType:  "gpu_max",
			hostState: &HostState{},
		},
		{
			name:     "all temperature samples are filtered",
			ruleType: "temperature_max",
			hostState: &HostState{Temperatures: []SensorTemperature{
				{Name: "coretemp", Temperature: 0},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{Common: Common{ID: 1}, State: test.hostState, Host: &Host{}}
			rule := &Rule{Type: test.ruleType, Max: 1, Duration: 3, Cover: RuleCoverAll}

			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("Snapshot panicked for missing metric data: %v", recovered)
				}
			}()
			if passed := rule.Snapshot(nil, server, nil); !passed {
				t.Fatal("missing metric data must be ignored instead of treated as a failed threshold")
			}
		})
	}
}

func TestAlertRuleCheckRejectsOverflowingPersistedDuration(t *testing.T) {
	rule := &AlertRule{Rules: []*Rule{{
		Type:     "offline",
		Duration: math.MaxUint64,
		Cover:    RuleCoverAll,
	}}}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Check panicked after uint64-to-int overflow: %v", recovered)
		}
	}()
	duration, passed := rule.Check([][]bool{{false}})
	if duration != 0 || !passed {
		t.Fatalf("invalid persisted duration must be ignored safely, got duration=%d passed=%v", duration, passed)
	}
	if window := rule.RetentionWindow(); window != 0 {
		t.Fatalf("invalid persisted duration must not create a retention window, got %d", window)
	}
}

func TestAlertRulePersistedDataSafety(t *testing.T) {
	cycleStart := time.Now().Add(-time.Hour)
	tests := []struct {
		name string
		rule *AlertRule
		want bool
	}{
		{
			name: "normal rule",
			rule: &AlertRule{Rules: []*Rule{{
				Type: "cpu", Duration: 3, Cover: RuleCoverAll,
			}}},
			want: true,
		},
		{
			name: "normal cycle rule",
			rule: &AlertRule{Rules: []*Rule{{
				Type: "transfer_in_cycle", CycleStart: &cycleStart, CycleInterval: 1, Cover: RuleCoverAll,
			}}},
			want: true,
		},
		{
			name: "unknown type",
			rule: &AlertRule{Rules: []*Rule{{
				Type: "attacker_controlled", Duration: 3, Cover: RuleCoverAll,
			}}},
		},
		{
			name: "overflowing duration",
			rule: &AlertRule{Rules: []*Rule{{
				Type: "offline", Duration: math.MaxUint64, Cover: RuleCoverAll,
			}}},
		},
		{
			name: "cycle without start",
			rule: &AlertRule{Rules: []*Rule{{
				Type: "transfer_in_cycle", CycleInterval: 1, Cover: RuleCoverAll,
			}}},
		},
		{
			name: "nil rule",
			rule: &AlertRule{Rules: []*Rule{nil}},
		},
		{
			name: "empty rule list",
			rule: &AlertRule{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.rule.IsSafeToEvaluate(); got != test.want {
				t.Fatalf("IsSafeToEvaluate()=%v, want %v", got, test.want)
			}
		})
	}
}
