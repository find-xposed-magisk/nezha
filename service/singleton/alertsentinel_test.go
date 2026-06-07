package singleton

import (
	"testing"

	"github.com/nezhahq/nezha/model"
)

// notifyDecision replays the exact send-gate from checkStatus (lines 164-186)
// for a single (alert, server) pair: given the current Check verdict and the
// previous stored state, it reports whether an incident or recovery notification
// would be dispatched and what the next stored state becomes. Kept in lockstep
// with checkStatus so the end-to-end "does it actually notify" path is testable
// without the DB/global singletons checkStatus pulls in.
func notifyDecision(triggerMode uint8, passed bool, prev uint8) (incident, recover bool, next uint8) {
	if !passed {
		if triggerMode == model.ModeAlwaysTrigger || prev != _RuleCheckFail {
			return true, false, _RuleCheckFail
		}
		return false, false, _RuleCheckFail
	}
	if prev == _RuleCheckFail {
		return false, true, _RuleCheckPass
	}
	return false, false, _RuleCheckPass
}

// driveCheckStatus simulates the checkStatus tick loop end-to-end: each tick
// appends one sample, runs the real Check, applies the real RetentionWindow
// trim, then runs the send-gate. It returns how many incident notifications
// would have been dispatched across all ticks and the final sample window size.
func driveCheckStatus(rule *model.AlertRule, triggerMode uint8, ticks int, sample []bool) (incidents int, finalWindow int) {
	var samples [][]bool
	prev := uint8(_RuleCheckNoData)
	for i := 0; i < ticks; i++ {
		samples = append(samples, append([]bool(nil), sample...))
		_, passed := rule.Check(samples)
		w := rule.RetentionWindow()
		if w <= 0 {
			samples = samples[:0]
		} else if w < len(samples) {
			samples = samples[len(samples)-w:]
		}
		incident, _, next := notifyDecision(triggerMode, passed, prev)
		if incident {
			incidents++
		}
		prev = next
	}
	return incidents, len(samples)
}

// TestCheckStatus_GeneralRuleFiresIncident is the end-to-end guard for the
// regression: a Duration:10 rule on a server that fails every tick must,
// after the window fills, reach passed=false and actually dispatch an incident
// notification. Before the fix the window was wiped each tick, so passed never
// became false and zero notifications were sent.
func TestCheckStatus_GeneralRuleFiresIncident(t *testing.T) {
	rule := &model.AlertRule{Rules: []*model.Rule{{Type: "cpu", Duration: 10}}}

	t.Run("AlwaysTrigger fires repeatedly once window fills", func(t *testing.T) {
		incidents, window := driveCheckStatus(rule, model.ModeAlwaysTrigger, 30, []bool{false})
		if window < 10 {
			t.Fatalf("window never filled: got %d want >= 10", window)
		}
		if incidents == 0 {
			t.Fatalf("AlwaysTrigger rule never dispatched an incident notification")
		}
	})

	t.Run("OnetimeTrigger fires exactly once", func(t *testing.T) {
		incidents, _ := driveCheckStatus(rule, model.ModeOnetimeTrigger, 30, []bool{false})
		if incidents != 1 {
			t.Fatalf("OnetimeTrigger must dispatch exactly one incident, got %d", incidents)
		}
	})
}

// TestCheckStatus_HealthyServerStaysSilent guards the other direction: a server
// passing every tick must never dispatch an incident.
func TestCheckStatus_HealthyServerStaysSilent(t *testing.T) {
	rule := &model.AlertRule{Rules: []*model.Rule{{Type: "cpu", Duration: 10}}}
	incidents, _ := driveCheckStatus(rule, model.ModeAlwaysTrigger, 30, []bool{true})
	if incidents != 0 {
		t.Fatalf("a healthy server must never trigger an incident, got %d", incidents)
	}
}
