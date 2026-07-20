//go:build linux

package scenario

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestHeldSessionAdapterCanceledWaiterRetainsCleanupForLaterCaller(t *testing.T) {
	cleanupStarted := make(chan struct{})
	cleanupRelease := make(chan struct{})
	cleanupErr := errors.New("cleanup failed")
	var cleanupCount atomic.Int32
	lifecycle := heldTestLifecycle(t, "")
	adapter := &heldSessionAdapter{
		lifecycle: lifecycle,
		cleanup: func(cleanupContext context.Context) error {
			cleanupCount.Add(1)
			if err := cleanupContext.Err(); err != nil {
				t.Errorf("cleanup context canceled before release: %v", err)
			}
			close(cleanupStarted)
			<-cleanupRelease
			return cleanupErr
		},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	firstResult := make(chan error, 1)
	go func() { firstResult <- adapter.Close(canceled) }()
	<-cleanupStarted
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Close error = %v", err)
	}
	close(cleanupRelease)
	if err := adapter.Close(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("later Close error = %v", err)
	}
	if count := cleanupCount.Load(); count != 1 {
		t.Fatalf("cleanup invocation count = %d, want 1", count)
	}
}

type heldSessionAdapter struct {
	lifecycle *heldSessionLifecycle
	cleanup   func(context.Context) error
}

func (adapter *heldSessionAdapter) Plan() StressSessionPlan { return adapter.lifecycle.Plan() }

func (adapter *heldSessionAdapter) WaitLive(ctx context.Context) error {
	return adapter.lifecycle.WaitLive(ctx)
}

func (adapter *heldSessionAdapter) IOStreamID() (string, bool) { return adapter.lifecycle.IOStreamID() }

func (adapter *heldSessionAdapter) WaitClosed(ctx context.Context) error {
	return adapter.lifecycle.WaitClosed(ctx)
}

func (adapter *heldSessionAdapter) Done() <-chan struct{} { return adapter.lifecycle.Done() }
func (adapter *heldSessionAdapter) CloseResult() error    { return adapter.lifecycle.CloseResult() }

func (adapter *heldSessionAdapter) Close(ctx context.Context) error {
	owner, won := adapter.lifecycle.beginClose()
	if !won {
		return adapter.lifecycle.WaitClosed(ctx)
	}
	go func() {
		cleanupContext, cancel := owner.cleanupContext()
		defer cancel()
		owner.markClosed(adapter.cleanup(cleanupContext))
	}()
	return adapter.lifecycle.WaitClosed(ctx)
}

var _ heldSession = (*heldSessionAdapter)(nil)
