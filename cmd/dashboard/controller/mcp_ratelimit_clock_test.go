package controller

import (
	"sync"
	"testing"
	"time"
)

type linearizationClock struct {
	mu     sync.Mutex
	times  []time.Time
	latest time.Time
}

func newLinearizationClock(oldTime, newTime time.Time) *linearizationClock {
	return &linearizationClock{
		times:  []time.Time{oldTime, oldTime, newTime},
		latest: newTime,
	}
}

func (clock *linearizationClock) Now() time.Time {
	clock.mu.Lock()
	now := clock.latest
	if len(clock.times) > 0 {
		now = clock.times[0]
		clock.times = clock.times[1:]
	}
	clock.mu.Unlock()
	return now
}

func TestLinearizationClockReturnsLatestTimeAfterSequenceIsConsumed(t *testing.T) {
	oldTime := time.Unix(1_700_000_000, 0)
	newTime := oldTime.Add(time.Second)
	clock := newLinearizationClock(oldTime, newTime)

	for range 3 {
		clock.Now()
	}

	if got := clock.Now(); !got.Equal(newTime) {
		t.Fatalf("clock returned %s after sequence was consumed, want %s", got, newTime)
	}
}
