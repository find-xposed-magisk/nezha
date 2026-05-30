package controller

import (
	"testing"
	"time"
)

// H8 regression: anonymous transfer failures (entry=nil — token was bogus
// or already consumed) must be sampled, not written to audit one-for-one.
// Otherwise any unauthenticated attacker can flood the audit table by
// repeatedly POSTing /mcp/upload/garbage.
func TestTransferAnonAuditThrottle_FirstRequestPasses(t *testing.T) {
	th := newTransferAnonAuditThrottle(10*time.Second, 5)

	if !th.shouldRecord("1.2.3.4") {
		t.Fatal("first anon failure from an IP must be recorded")
	}
}

func TestTransferAnonAuditThrottle_BurstCappedPerWindow(t *testing.T) {
	th := newTransferAnonAuditThrottle(time.Minute, 3)
	const ip = "5.6.7.8"

	recorded := 0
	for i := 0; i < 20; i++ {
		if th.shouldRecord(ip) {
			recorded++
		}
	}
	if recorded > 3 {
		t.Fatalf("burst of 20 anon failures must be capped at 3 per window, got %d", recorded)
	}
	if recorded == 0 {
		t.Fatal("burst must record at least one sample")
	}
}

func TestTransferAnonAuditThrottle_IndependentPerIP(t *testing.T) {
	th := newTransferAnonAuditThrottle(time.Minute, 1)

	if !th.shouldRecord("a") {
		t.Fatal("first request from IP a must be recorded")
	}
	if !th.shouldRecord("b") {
		t.Fatal("first request from a different IP must not share IP a's budget")
	}
	if th.shouldRecord("a") {
		t.Fatal("second request from IP a within window must be dropped")
	}
}

func TestTransferAnonAuditThrottle_WindowResets(t *testing.T) {
	th := newTransferAnonAuditThrottle(20*time.Millisecond, 1)
	const ip = "9.9.9.9"

	if !th.shouldRecord(ip) {
		t.Fatal("first request must be recorded")
	}
	if th.shouldRecord(ip) {
		t.Fatal("second request inside window must be dropped")
	}

	time.Sleep(40 * time.Millisecond)

	if !th.shouldRecord(ip) {
		t.Fatal("request after window expiry must be recorded again")
	}
}
