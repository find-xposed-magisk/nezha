package controller

import (
	"testing"
	"time"
)

// The per-token limiter map had no eviction: every distinct token ID ever
// seen left a permanent entry. A user churning PATs (create/use/delete in a
// loop) grows the map without bound. Allow must opportunistically prune
// windows idle past the minute bucket so memory stays proportional to the
// active token set, not the historical one.
func TestMCPRateLimiter_PrunesStaleTokenWindows(t *testing.T) {
	rl := newMCPRateLimiter(10, 120)

	stale := time.Now().Add(-10 * time.Minute)
	for i := uint64(1); i <= 500; i++ {
		rl.mu.Lock()
		rl.perToken[i] = &tokenWindow{
			secBucketStart: stale,
			minBucketStart: stale,
		}
		rl.mu.Unlock()
	}

	// A fresh request triggers a prune sweep of idle windows.
	if !rl.Allow(99999) {
		t.Fatal("fresh token must be allowed")
	}

	rl.mu.Lock()
	size := len(rl.perToken)
	rl.mu.Unlock()

	// Only the just-active token (99999) should remain; the 500 stale ones
	// must have been evicted.
	if size > 1 {
		t.Fatalf("stale token windows were not pruned: map still holds %d entries", size)
	}
}

// Pruning must NOT evict tokens that are still within their active window,
// otherwise an in-flight client loses its accumulated count and effectively
// resets its budget.
func TestMCPRateLimiter_KeepsActiveTokenWindows(t *testing.T) {
	rl := newMCPRateLimiter(10, 120)

	if !rl.Allow(1) {
		t.Fatal("token 1 must be allowed")
	}
	if !rl.Allow(2) {
		t.Fatal("token 2 must be allowed")
	}

	rl.mu.Lock()
	size := len(rl.perToken)
	rl.mu.Unlock()

	if size != 2 {
		t.Fatalf("active token windows must be retained, got %d entries", size)
	}
}
