//go:build linux

package scenario

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestHeldSessionLifecycleRejectsInvalidConstruction(t *testing.T) {
	validPlan := heldTestPlan(t)
	cases := []struct {
		name    string
		plan    StressSessionPlan
		timeout time.Duration
	}{
		{"zero-session-id", StressSessionPlan{Kind: validPlan.Kind, Ordinal: 1, Agent: validPlan.Agent}, time.Second},
		{"unsupported-kind", StressSessionPlan{ID: validPlan.ID, Kind: StressSessionKind("unsupported"), Ordinal: 1, Agent: validPlan.Agent}, time.Second},
		{"zero-ordinal", StressSessionPlan{ID: validPlan.ID, Kind: validPlan.Kind, Agent: validPlan.Agent}, time.Second},
		{"zero-agent", StressSessionPlan{ID: validPlan.ID, Kind: validPlan.Kind, Ordinal: 1}, time.Second},
		{"zero-timeout", validPlan, 0},
		{"nil-base-context", validPlan, time.Second},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			baseContext := context.Background()
			if testCase.name == "nil-base-context" {
				baseContext = nil
			}
			_, err := newHeldSessionLifecycle(baseContext, testCase.plan, "", testCase.timeout)
			if !errors.Is(err, ErrInvalidHeldSessionPlan) {
				t.Fatalf("construction error = %v", err)
			}
		})
	}
}

func TestHeldSessionLifecycleRetainsLiveResultAndOptionalIOStreamID(t *testing.T) {
	lifecycle := heldTestLifecycle(t, "io-stream-identity")
	if err := lifecycle.markLive(nil); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.WaitLive(context.Background()); err != nil {
		t.Fatal(err)
	}
	streamID, present := lifecycle.IOStreamID()
	if !present || streamID != "io-stream-identity" {
		t.Fatalf("IOStream identity = %q, %v", streamID, present)
	}
	owner, won := lifecycle.beginClose()
	if !won {
		t.Fatal("beginClose did not return owner")
	}
	owner.markClosed(nil)
	if err := lifecycle.WaitClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHeldSessionLifecycleFailedLiveStateRetainsExactError(t *testing.T) {
	liveErr := errors.New("session failed to become live")
	lifecycle := heldTestLifecycle(t, "")
	if err := lifecycle.markLive(liveErr); err != nil {
		t.Fatal(err)
	}
	if lifecycle.state != heldSessionFailed {
		t.Fatalf("state = %v, want failed", lifecycle.state)
	}
	if err := lifecycle.WaitLive(context.Background()); !errors.Is(err, liveErr) {
		t.Fatalf("WaitLive error = %v", err)
	}
	if err := lifecycle.markLive(nil); !errors.Is(err, ErrHeldSessionLiveResolved) {
		t.Fatalf("second markLive error = %v", err)
	}
}

func TestHeldSessionLifecycleCloseBeforeLiveRetainsFailure(t *testing.T) {
	lifecycle := heldTestLifecycle(t, "")
	owner, won := lifecycle.beginClose()
	if !won {
		t.Fatal("beginClose did not return owner")
	}
	if err := lifecycle.WaitLive(context.Background()); !errors.Is(err, ErrHeldSessionClosedBeforeLive) {
		t.Fatalf("WaitLive error = %v", err)
	}
	owner.markClosed(nil)
	if err := lifecycle.WaitClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHeldSessionLifecycleFailedLiveRetainsDistinctCleanupResult(t *testing.T) {
	liveErr := errors.New("live failed")
	cleanupErr := errors.New("cleanup failed")
	lifecycle := heldTestLifecycle(t, "")
	if err := lifecycle.markLive(liveErr); err != nil {
		t.Fatal(err)
	}
	owner, won := lifecycle.beginClose()
	if !won {
		t.Fatal("beginClose did not return owner")
	}
	owner.markClosed(cleanupErr)
	if err := lifecycle.WaitLive(context.Background()); !errors.Is(err, liveErr) {
		t.Fatalf("live error = %v", err)
	}
	if err := lifecycle.WaitClosed(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("closed error = %v", err)
	}
}

func TestHeldSessionLifecycleDoesNotImplementHeldSession(t *testing.T) {
	lifecycle := heldTestLifecycle(t, "")
	var candidate any = lifecycle
	if _, ok := candidate.(heldSession); ok {
		t.Fatal("lifecycle unexpectedly implements heldSession; cleanup ownership belongs to adapters")
	}
}

func TestHeldSessionLifecycleBeginCloseHasSingleWinner(t *testing.T) {
	lifecycle := heldTestLifecycle(t, "")
	owners := make(chan *heldSessionCloseOwner, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Go(func() {
			owner, won := lifecycle.beginClose()
			if won {
				owners <- owner
			}
		})
	}
	waitGroup.Wait()
	close(owners)
	var owner *heldSessionCloseOwner
	for candidate := range owners {
		if owner != nil {
			t.Fatal("beginClose returned two owners")
		}
		owner = candidate
	}
	if owner == nil {
		t.Fatal("beginClose returned no owner")
	}
	owner.markClosed(nil)
}

func TestHeldSessionCloseOwnerCleanupContextIgnoresParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	lifecycle := heldTestLifecycleWithBase(t, parent, time.Second)
	owner, won := lifecycle.beginClose()
	if !won {
		t.Fatal("beginClose did not return owner")
	}
	cleanupContext, cleanupCancel := owner.cleanupContext()
	defer cleanupCancel()
	if err := cleanupContext.Err(); err != nil {
		t.Fatalf("cleanup context already canceled: %v", err)
	}
	owner.markClosed(nil)
}

func heldTestPlan(t *testing.T) StressSessionPlan {
	t.Helper()
	id, err := NewStressSessionID("held-session")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := NewStressAgentOrdinal(1)
	if err != nil {
		t.Fatal(err)
	}
	return StressSessionPlan{ID: id, Kind: StressSessionTerminal, Ordinal: 1, Agent: agent}
}

func heldTestLifecycle(t *testing.T, streamID string) *heldSessionLifecycle {
	return heldTestLifecycleWithBaseAndID(t, context.Background(), streamID, time.Second)
}

func heldTestLifecycleWithBase(t *testing.T, base context.Context, timeout time.Duration) *heldSessionLifecycle {
	return heldTestLifecycleWithBaseAndID(t, base, "", timeout)
}

func heldTestLifecycleWithBaseAndID(t *testing.T, base context.Context, streamID string, timeout time.Duration) *heldSessionLifecycle {
	lifecycle, err := newHeldSessionLifecycle(base, heldTestPlan(t), streamID, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle
}
