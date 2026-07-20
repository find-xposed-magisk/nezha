package controller

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mcpRateLimitFakeClock struct {
	now time.Time
}

func (clock *mcpRateLimitFakeClock) Now() time.Time {
	return clock.now
}

func (clock *mcpRateLimitFakeClock) Advance(duration time.Duration) {
	clock.now = clock.now.Add(duration)
}

func TestMCPRateLimiter_baselinePreservesAnonymousBypassAndBucketReset(t *testing.T) {
	// Given
	limiter := newMCPRateLimiter(3, 3)

	// When
	anonymousAllowed := limiter.Allow(0)
	firstAllowed := limiter.Allow(7)
	secondAllowed := limiter.Allow(7)
	thirdAllowed := limiter.Allow(7)
	fourthRejected := limiter.Allow(7)
	// Then
	if !anonymousAllowed || !firstAllowed || !secondAllowed || !thirdAllowed || fourthRejected {
		t.Fatalf("baseline limiter semantics changed: anonymous=%t first=%t second=%t third=%t rejected=%t", anonymousAllowed, firstAllowed, secondAllowed, thirdAllowed, fourthRejected)
	}
}

func TestMCPRateLimiter_allowsTenAndRejectsElevenWithinOneSecond(t *testing.T) {
	// Given
	clock := &mcpRateLimitFakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newMCPRateLimiterWithClock(10, 120, clock.Now)

	// When
	allowed := 0
	for requestNumber := 1; requestNumber <= 11; requestNumber++ {
		if limiter.Allow(7) {
			allowed++
		}
	}

	// Then
	if allowed != 10 {
		t.Fatalf("allowed count = %d, want 10", allowed)
	}
	if limiter.Allow(7) {
		t.Fatal("twelfth request must remain rejected in the same second")
	}
}

func TestMCPRateLimiter_allowsOneAfterSecondBucketRollover(t *testing.T) {
	// Given
	clock := &mcpRateLimitFakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newMCPRateLimiterWithClock(10, 120, clock.Now)
	for requestNumber := 1; requestNumber <= 10; requestNumber++ {
		if !limiter.Allow(7) {
			t.Fatalf("request %d must be allowed before rollover", requestNumber)
		}
	}

	// When
	clock.Advance(time.Second)

	// Then
	if !limiter.Allow(7) {
		t.Fatal("request after one-second bucket rollover must be allowed")
	}
}

func TestMCPRateLimiter_allows120AndRejects121WithinOneMinute(t *testing.T) {
	// Given
	clock := &mcpRateLimitFakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newMCPRateLimiterWithClock(10_000, 120, clock.Now)

	// When
	allowed := 0
	for requestNumber := 1; requestNumber <= 121; requestNumber++ {
		if limiter.Allow(7) {
			allowed++
		}
		clock.Advance(100 * time.Millisecond)
	}

	// Then
	if allowed != 120 {
		t.Fatalf("allowed count = %d, want 120", allowed)
	}
}

func TestMCPRateLimiter_independentTokensKeepIndependentBudgets(t *testing.T) {
	// Given
	clock := &mcpRateLimitFakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newMCPRateLimiterWithClock(1, 120, clock.Now)

	// When
	firstTokenAllowed := limiter.Allow(7)
	firstTokenRejected := limiter.Allow(7)
	secondTokenAllowed := limiter.Allow(8)

	// Then
	if !firstTokenAllowed || firstTokenRejected || !secondTokenAllowed {
		t.Fatalf("token budgets are not independent: first allowed=%t rejected=%t second allowed=%t", firstTokenAllowed, firstTokenRejected, secondTokenAllowed)
	}
}

func TestMCPRateLimiter_clockControlledCallsDoNotMutateSharedLimiter(t *testing.T) {
	// Given
	originalLimiter := mcpRateLimiterShared
	clock := &mcpRateLimitFakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newMCPRateLimiterWithClock(1, 1, clock.Now)

	// When
	if !limiter.Allow(7) {
		t.Fatal("isolated limiter request must be allowed")
	}

	// Then
	if mcpRateLimiterShared != originalLimiter {
		t.Fatal("isolated limiter changed shared production limiter ownership")
	}
}

func TestMCPRateLimiter_concurrentCallsAtSecondBoundaryAllowExactlyLimit(t *testing.T) {
	// Given
	clock := &mcpRateLimitFakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newMCPRateLimiterWithClock(10, 120, clock.Now)
	var allowed atomic.Int32
	var waitGroup sync.WaitGroup

	// When
	for requestNumber := 0; requestNumber < 20; requestNumber++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if limiter.Allow(7) {
				allowed.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	// Then
	if got := allowed.Load(); got != 10 {
		t.Fatalf("concurrent allowed count = %d, want 10", got)
	}
}

func TestMCPRateLimiter_minuteBucketRolloverRestoresBudget(t *testing.T) {
	// Given
	clock := &mcpRateLimitFakeClock{now: time.Unix(1_700_000_000, 0)}
	limiter := newMCPRateLimiterWithClock(10_000, 1, clock.Now)
	if !limiter.Allow(7) {
		t.Fatal("first request must be allowed")
	}
	if limiter.Allow(7) {
		t.Fatal("second request must be rejected before minute rollover")
	}

	// When
	clock.Advance(time.Minute)

	// Then
	if !limiter.Allow(7) {
		t.Fatal("request after minute rollover must be allowed")
	}
}

func TestMCPRateLimiter_samplesClockWhileHoldingStateLock(t *testing.T) {
	// Given
	now := time.Unix(1_700_000_000, 0)
	var limiter *MCPRateLimiter
	clock := func() time.Time {
		if limiter.mu.TryLock() {
			limiter.mu.Unlock()
			t.Fatal("clock callback acquired limiter state lock; Allow sampled before locking")
		}
		return now
	}
	limiter = newMCPRateLimiterWithClock(1, 1, clock)

	// When
	allowed := limiter.Allow(7)

	// Then
	if !allowed {
		t.Fatal("initial request must be allowed")
	}
}
