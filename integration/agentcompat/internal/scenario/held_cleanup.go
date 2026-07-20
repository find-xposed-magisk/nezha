//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrInvalidHeldCleanupAction = errors.New("held cleanup action is invalid")
	ErrHeldCleanupClosed        = errors.New("held cleanup stack is closed")
)

type heldCleanupAction struct {
	name    string
	cleanup func(context.Context) error
}

type heldCleanupStackState uint8

const (
	heldCleanupOpen heldCleanupStackState = iota
	heldCleanupRunning
	heldCleanupClosed
)

type heldCleanupStack struct {
	mu      sync.Mutex
	state   heldCleanupStackState
	actions []heldCleanupAction
}

func newHeldCleanupStack() *heldCleanupStack {
	return &heldCleanupStack{state: heldCleanupOpen}
}

func (stack *heldCleanupStack) Push(action heldCleanupAction) error {
	if action.name == "" || action.cleanup == nil {
		return ErrInvalidHeldCleanupAction
	}
	stack.mu.Lock()
	defer stack.mu.Unlock()
	if stack.state != heldCleanupOpen {
		return ErrHeldCleanupClosed
	}
	stack.actions = append(stack.actions, action)
	return nil
}

func (stack *heldCleanupStack) Run(ctx context.Context) error {
	stack.mu.Lock()
	if stack.state != heldCleanupOpen {
		stack.mu.Unlock()
		return ErrHeldCleanupClosed
	}
	stack.state = heldCleanupRunning
	actions := append([]heldCleanupAction(nil), stack.actions...)
	stack.mu.Unlock()

	var joined error
	for index := len(actions) - 1; index >= 0; index-- {
		action := actions[index]
		if err := action.cleanup(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("cleanup %s: %w", action.name, err))
		}
	}

	stack.mu.Lock()
	stack.state = heldCleanupClosed
	stack.mu.Unlock()
	return joined
}
