//go:build linux && agentcompat

package scenario

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

func runStressRound(ctx context.Context, fixture *heldSessionSetRealFixture, plan StressRoundPlan) (StressRoundEvidence, error) {
	registry, err := newStressExactOnceRegistry(StressPlan{Rounds: []StressRoundPlan{plan}})
	if err != nil {
		return StressRoundEvidence{}, err
	}
	ready := make(chan struct{}, len(plan.Operations))
	release := make(chan struct{})
	results := make(chan StressOperationEvidence, len(plan.Operations))
	var workers sync.WaitGroup
	for _, operation := range plan.Operations {
		operation := operation
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready <- struct{}{}
			<-release
			if startErr := registry.Start(operation); startErr != nil {
				results <- StressOperationEvidence{ID: operation.ID, Round: operation.Round, Agent: operation.Agent, PAT: operation.PAT, Kind: operation.Kind, Error: startErr.Error()}
				return
			}
			results <- stressOperationExecutor{fixture: fixture, plan: operation}.run(ctx)
		}()
	}
	for range plan.Operations {
		select {
		case <-ready:
		case <-ctx.Done():
			close(release)
			workers.Wait()
			return StressRoundEvidence{}, ctx.Err()
		}
	}
	close(release)
	workers.Wait()
	byID := make(map[StressOperationID]StressOperationEvidence, len(plan.Operations))
	for range plan.Operations {
		select {
		case operation := <-results:
			if _, duplicate := byID[operation.ID]; duplicate {
				return StressRoundEvidence{}, fmt.Errorf("duplicate result for operation %s", operation.ID.String())
			}
			byID[operation.ID] = operation
		case <-ctx.Done():
			return StressRoundEvidence{}, ctx.Err()
		}
	}
	evidenceValue := StressRoundEvidence{Round: plan.Round, Operations: make([]StressOperationEvidence, len(plan.Operations))}
	for index, operation := range plan.Operations {
		result, exists := byID[operation.ID]
		if !exists {
			return StressRoundEvidence{}, ErrStressOperationMissingCompletion
		}
		evidenceValue.Operations[index] = result
		if result.Error != "" {
			return StressRoundEvidence{}, errors.Join(fmt.Errorf("operation %s (%s) failed: %s", result.ID.String(), result.Kind, result.Error), stressRoundErrors(evidenceValue))
		}
		if result.Error == "" {
			if err := registry.Complete(StressOperationReceipt{Operation: operation, SuccessProof: result.SuccessProof}); err != nil {
				return StressRoundEvidence{}, err
			}
		}
	}
	if err := registry.ValidateComplete(); err != nil {
		return StressRoundEvidence{}, err
	}
	if err := ValidateStressRoundEvidence(plan, evidenceValue); err != nil {
		return StressRoundEvidence{}, errors.Join(err, stressRoundErrors(evidenceValue))
	}
	return evidenceValue, nil
}

func stressRoundErrors(evidenceValue StressRoundEvidence) error {
	var joined error
	for _, operation := range evidenceValue.Operations {
		if operation.Error != "" {
			joined = errors.Join(joined, errors.New(operation.Error))
		}
	}
	return joined
}
