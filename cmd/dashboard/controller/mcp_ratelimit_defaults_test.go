package controller

import "testing"

// TestMCPRateLimiter_DefaultsAreDoubledFromInitialBaseline pins the
// production-side per-token budget. The first iteration shipped 5/s + 60/min,
// which gated legitimate LLM bursts more aggressively than the audit /
// concurrency story required. Doubling to 10/s + 120/min keeps the bucket
// shape (same window, same per-token bookkeeping) so observed behavior
// regressions stay attributable to budget rather than algorithm changes.
func TestMCPRateLimiter_DefaultsAreDoubledFromInitialBaseline(t *testing.T) {
	if mcpRateLimiterShared.secLimit != 10 {
		t.Fatalf("default per-second limit = %d, want 10", mcpRateLimiterShared.secLimit)
	}
	if mcpRateLimiterShared.minLimit != 120 {
		t.Fatalf("default per-minute limit = %d, want 120", mcpRateLimiterShared.minLimit)
	}
}
