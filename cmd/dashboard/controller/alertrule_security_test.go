package controller

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func TestValidateRuleRejectsCrashPayloadsFromMember(t *testing.T) {
	ctx := newMemberValidationContext(t)
	cycleStart := time.Now().Add(-time.Hour)
	tests := []struct {
		name string
		rule *model.Rule
	}{
		{
			name: "unknown rule type",
			rule: &model.Rule{Type: "attacker_controlled", Duration: 3, Cover: model.RuleCoverAll},
		},
		{
			name: "duration overflows int",
			rule: &model.Rule{Type: "offline", Duration: math.MaxUint64, Cover: model.RuleCoverAll},
		},
		{
			name: "cycle interval overflows int",
			rule: &model.Rule{
				Type:          "transfer_in_cycle",
				CycleStart:    &cycleStart,
				CycleInterval: math.MaxUint64,
				Cover:         model.RuleCoverAll,
			},
		},
		{
			name: "nil rule",
			rule: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			alert := &model.AlertRule{
				Common: model.Common{UserID: 200},
				Name:   "security regression",
				Rules:  []*model.Rule{test.rule},
			}
			require.Error(t, validateRule(ctx, alert))
		})
	}
}

func TestValidateRuleAcceptsSafeDurationBoundary(t *testing.T) {
	ctx := newMemberValidationContext(t)
	alert := &model.AlertRule{
		Common: model.Common{UserID: 200},
		Name:   "duration boundary",
		Rules: []*model.Rule{{
			Type:     "offline",
			Duration: model.MaxAlertRuleDuration,
			Cover:    model.RuleCoverAll,
		}},
	}
	require.NoError(t, validateRule(ctx, alert))
}
