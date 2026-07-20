//go:build linux

package scenario

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrStressOperationUnknown             = errors.New("stress operation is unknown")
	ErrStressOperationDuplicateStart      = errors.New("stress operation started more than once")
	ErrStressOperationDuplicateCompletion = errors.New("stress operation completed more than once")
	ErrStressOperationOwnerMismatch       = errors.New("stress operation owner does not match plan")
	ErrStressOperationMissingCompletion   = errors.New("stress operation completion is missing")
)

type StressOperationReceipt struct {
	Operation    StressOperationPlan `json:"operation"`
	SuccessProof string              `json:"success_proof"`
}

type stressOperationState struct {
	plan      StressOperationPlan
	started   bool
	completed bool
}

type stressExactOnceRegistry struct {
	mu         sync.Mutex
	operations map[StressOperationID]*stressOperationState
}

func newStressExactOnceRegistry(plan StressPlan) (*stressExactOnceRegistry, error) {
	operations := make(map[StressOperationID]*stressOperationState)
	for _, round := range plan.Rounds {
		for _, operation := range round.Operations {
			if operation.ID.String() == "" {
				return nil, fmt.Errorf("empty operation ID: %w", ErrStressOperationUnknown)
			}
			if _, exists := operations[operation.ID]; exists {
				return nil, fmt.Errorf("duplicate operation ID %s: %w", operation.ID.String(), ErrStressOperationUnknown)
			}
			operations[operation.ID] = &stressOperationState{plan: operation}
		}
	}
	return &stressExactOnceRegistry{operations: operations}, nil
}

func (registry *stressExactOnceRegistry) Start(operation StressOperationPlan) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, exists := registry.operations[operation.ID]
	if !exists {
		return ErrStressOperationUnknown
	}
	if state.plan != operation {
		return ErrStressOperationOwnerMismatch
	}
	if state.started {
		return ErrStressOperationDuplicateStart
	}
	state.started = true
	return nil
}

func (registry *stressExactOnceRegistry) Complete(receipt StressOperationReceipt) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	state, exists := registry.operations[receipt.Operation.ID]
	if !exists {
		return ErrStressOperationUnknown
	}
	if state.plan != receipt.Operation {
		return ErrStressOperationOwnerMismatch
	}
	if !state.started {
		return ErrStressOperationDuplicateStart
	}
	if state.completed {
		return ErrStressOperationDuplicateCompletion
	}
	if receipt.SuccessProof == "" {
		return ErrStressOperationMissingCompletion
	}
	state.completed = true
	return nil
}

func (registry *stressExactOnceRegistry) ValidateComplete() error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, state := range registry.operations {
		if !state.started || !state.completed {
			return ErrStressOperationMissingCompletion
		}
	}
	return nil
}
