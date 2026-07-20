//go:build linux

package scenario

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidHeldSessionPlan      = errors.New("held session plan is invalid")
	ErrHeldSessionClosedBeforeLive = errors.New("held session closed before live")
	ErrHeldSessionLiveResolved     = errors.New("held session live state already resolved")
)

type heldSession interface {
	Plan() StressSessionPlan
	WaitLive(context.Context) error
	Close(context.Context) error
	WaitClosed(context.Context) error
	IOStreamID() (string, bool)
	Done() <-chan struct{}
	CloseResult() error
}

type heldSessionState uint8

const (
	heldSessionConstructed heldSessionState = iota
	heldSessionLive
	heldSessionFailed
	heldSessionClosing
	heldSessionClosed
)

type heldSessionLifecycle struct {
	baseContext    context.Context
	plan           StressSessionPlan
	ioStreamID     string
	cleanupTimeout time.Duration

	mu           sync.Mutex
	state        heldSessionState
	liveResult   error
	liveDone     chan struct{}
	closedDone   chan struct{}
	closedResult error
}

type heldSessionCloseOwner struct {
	lifecycle *heldSessionLifecycle
	closeOnce sync.Once
}

func newHeldSessionLifecycle(baseContext context.Context, plan StressSessionPlan, ioStreamID string, cleanupTimeout time.Duration) (*heldSessionLifecycle, error) {
	if baseContext == nil || plan.ID.String() == "" || !supportedHeldSessionKind(plan.Kind) || plan.Ordinal < 1 || plan.Agent.Int() < 1 || cleanupTimeout <= 0 {
		return nil, ErrInvalidHeldSessionPlan
	}
	return &heldSessionLifecycle{
		baseContext:    baseContext,
		plan:           plan,
		ioStreamID:     ioStreamID,
		cleanupTimeout: cleanupTimeout,
		state:          heldSessionConstructed,
		liveDone:       make(chan struct{}),
		closedDone:     make(chan struct{}),
	}, nil
}

func supportedHeldSessionKind(kind StressSessionKind) bool {
	switch kind {
	case StressSessionTerminal, StressSessionNAT, StressSessionFM:
		return true
	default:
		return false
	}
}

func (lifecycle *heldSessionLifecycle) Plan() StressSessionPlan { return lifecycle.plan }

func (lifecycle *heldSessionLifecycle) IOStreamID() (string, bool) {
	return lifecycle.ioStreamID, lifecycle.ioStreamID != ""
}

func (lifecycle *heldSessionLifecycle) setIOStreamID(streamID string) error {
	if streamID == "" {
		return errors.New("held session stream ID is empty")
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.state != heldSessionConstructed {
		return ErrHeldSessionLiveResolved
	}
	lifecycle.ioStreamID = streamID
	return nil
}

func (lifecycle *heldSessionLifecycle) markLive(err error) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.state != heldSessionConstructed {
		return ErrHeldSessionLiveResolved
	}
	lifecycle.liveResult = err
	if err == nil {
		lifecycle.state = heldSessionLive
	} else {
		lifecycle.state = heldSessionFailed
	}
	close(lifecycle.liveDone)
	return nil
}

func (lifecycle *heldSessionLifecycle) WaitLive(ctx context.Context) error {
	select {
	case <-lifecycle.liveDone:
		lifecycle.mu.Lock()
		defer lifecycle.mu.Unlock()
		return lifecycle.liveResult
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (lifecycle *heldSessionLifecycle) beginClose() (*heldSessionCloseOwner, bool) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.state == heldSessionConstructed {
		lifecycle.liveResult = ErrHeldSessionClosedBeforeLive
		lifecycle.state = heldSessionClosing
		close(lifecycle.liveDone)
		return &heldSessionCloseOwner{lifecycle: lifecycle}, true
	}
	if lifecycle.state == heldSessionLive || lifecycle.state == heldSessionFailed {
		lifecycle.state = heldSessionClosing
		return &heldSessionCloseOwner{lifecycle: lifecycle}, true
	}
	return nil, false
}

func (owner *heldSessionCloseOwner) cleanupContext() (context.Context, context.CancelFunc) {
	// The deadline only signals cancellation; it cannot forcibly terminate arbitrary cleanup work.
	return context.WithTimeout(context.WithoutCancel(owner.lifecycle.baseContext), owner.lifecycle.cleanupTimeout)
}

func (owner *heldSessionCloseOwner) markClosed(err error) {
	owner.closeOnce.Do(func() {
		lifecycle := owner.lifecycle
		lifecycle.mu.Lock()
		lifecycle.closedResult = err
		lifecycle.state = heldSessionClosed
		close(lifecycle.closedDone)
		lifecycle.mu.Unlock()
	})
}

func (lifecycle *heldSessionLifecycle) WaitClosed(ctx context.Context) error {
	select {
	case <-lifecycle.closedDone:
		lifecycle.mu.Lock()
		defer lifecycle.mu.Unlock()
		return lifecycle.closedResult
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (lifecycle *heldSessionLifecycle) Done() <-chan struct{} { return lifecycle.closedDone }

func (lifecycle *heldSessionLifecycle) CloseResult() error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.closedResult
}
