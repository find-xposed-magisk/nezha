//go:build linux

package scenario

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrStressOperationSet = errors.New("stress operation evidence set is invalid")
	ErrStressLaunchWindow = errors.New("stress operation launch window exceeded one second")
)

const stressLaunchWindow = time.Second

type StressOperationEvidence struct {
	ID           StressOperationID   `json:"id"`
	Round        int                 `json:"round"`
	Agent        StressAgentOrdinal  `json:"agent"`
	PAT          StressPATID         `json:"pat_id"`
	Kind         StressOperationKind `json:"kind"`
	LaunchedAt   time.Time           `json:"launched_at"`
	CompletedAt  time.Time           `json:"completed_at"`
	Succeeded    bool                `json:"succeeded"`
	SuccessProof string              `json:"success_proof"`
	Error        string              `json:"error,omitempty"`
}

type StressRoundEvidence struct {
	Round      int                       `json:"round"`
	Operations []StressOperationEvidence `json:"operations"`
}

func ValidateStressRoundEvidence(plan StressRoundPlan, evidence StressRoundEvidence) error {
	matched, err := matchStressRoundEvidence(plan, evidence)
	if err != nil {
		return err
	}
	for _, operation := range matched {
		if !operation.Succeeded || operation.SuccessProof == "" || operation.Error != "" {
			return fmt.Errorf("operation %s did not succeed: %w", operation.ID.String(), ErrStressOperationSet)
		}
	}
	return nil
}

func matchStressRoundEvidence(plan StressRoundPlan, evidence StressRoundEvidence) ([]StressOperationEvidence, error) {
	if evidence.Round != plan.Round || len(evidence.Operations) != len(plan.Operations) {
		return nil, fmt.Errorf("round=%d operations=%d want round=%d operations=%d: %w", evidence.Round, len(evidence.Operations), plan.Round, len(plan.Operations), ErrStressOperationSet)
	}
	expected := make(map[StressOperationID]struct{}, len(plan.Operations))
	for _, operation := range plan.Operations {
		expected[operation.ID] = struct{}{}
	}
	matched := make([]StressOperationEvidence, 0, len(evidence.Operations))
	seen := make(map[StressOperationID]struct{}, len(evidence.Operations))
	var firstLaunch, lastLaunch time.Time
	for index, operation := range evidence.Operations {
		planned := plan.Operations[index]
		if _, exists := expected[operation.ID]; !exists {
			return nil, fmt.Errorf("unexpected operation %s: %w", operation.ID.String(), ErrStressOperationSet)
		}
		if operation.Round != planned.Round || operation.Agent != planned.Agent || operation.PAT != planned.PAT || operation.Kind != planned.Kind || operation.ID != planned.ID {
			return nil, fmt.Errorf("operation %s owner or order mismatch: %w", operation.ID.String(), ErrStressOperationSet)
		}
		if _, duplicate := seen[operation.ID]; duplicate {
			return nil, fmt.Errorf("duplicate operation %s: %w", operation.ID.String(), ErrStressOperationSet)
		}
		if operation.LaunchedAt.IsZero() || operation.CompletedAt.Before(operation.LaunchedAt) {
			return nil, fmt.Errorf("operation %s timing is invalid: %w", operation.ID.String(), ErrStressOperationSet)
		}
		if operation.Succeeded && operation.SuccessProof == "" {
			return nil, fmt.Errorf("operation %s proof is empty: %w", operation.ID.String(), ErrStressOperationSet)
		}
		seen[operation.ID] = struct{}{}
		matched = append(matched, operation)
		if firstLaunch.IsZero() || operation.LaunchedAt.Before(firstLaunch) {
			firstLaunch = operation.LaunchedAt
		}
		if lastLaunch.IsZero() || operation.LaunchedAt.After(lastLaunch) {
			lastLaunch = operation.LaunchedAt
		}
	}
	if lastLaunch.Sub(firstLaunch) > stressLaunchWindow {
		return nil, fmt.Errorf("launch window=%s: %w", lastLaunch.Sub(firstLaunch), ErrStressLaunchWindow)
	}
	return matched, nil
}
