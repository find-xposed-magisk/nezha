package controller

import (
	"context"
	"testing"
	"time"
)

// M7 regression: long-lived PAT-authenticated handlers (ws/server,
// ws/transfer, terminal, FM) must register a cancel hook so that
// deleteAPIToken can close active connections immediately. Without this,
// a revoked PAT keeps streaming until the connection naturally drops.
func TestPATConnectionRegistry_CancelsOnRevoke(t *testing.T) {
	registry := newPATConnectionRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deregister := registry.register(42, cancel)
	defer deregister()

	registry.revokeToken(42)

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("revokeToken must cancel the registered context within 1s")
	}
}

func TestPATConnectionRegistry_DoesNotCancelOtherTokens(t *testing.T) {
	registry := newPATConnectionRegistry()
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()

	deregisterA := registry.register(1, cancelA)
	deregisterB := registry.register(2, cancelB)
	defer deregisterA()
	defer deregisterB()

	registry.revokeToken(1)

	select {
	case <-ctxA.Done():
	case <-time.After(time.Second):
		t.Fatal("token 1's connection must be cancelled")
	}

	select {
	case <-ctxB.Done():
		t.Fatal("token 2's connection must NOT be cancelled (separate token)")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPATConnectionRegistry_DeregisterClearsEntry(t *testing.T) {
	registry := newPATConnectionRegistry()
	_, cancel := context.WithCancel(context.Background())
	deregister := registry.register(1, cancel)

	deregister()

	if got := registry.countForToken(1); got != 0 {
		t.Fatalf("after deregister, count for token 1 must be 0, got %d", got)
	}
}

func TestPATConnectionRegistry_MultipleConnsPerToken(t *testing.T) {
	registry := newPATConnectionRegistry()
	ctx1, c1 := context.WithCancel(context.Background())
	ctx2, c2 := context.WithCancel(context.Background())
	defer c1()
	defer c2()

	d1 := registry.register(7, c1)
	d2 := registry.register(7, c2)
	defer d1()
	defer d2()

	if got := registry.countForToken(7); got != 2 {
		t.Fatalf("expected 2 connections for token 7, got %d", got)
	}

	registry.revokeToken(7)

	for _, ctx := range []context.Context{ctx1, ctx2} {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("all connections for the revoked token must be cancelled")
		}
	}
}

func TestPATConnectionRegistry_RevokeUnknownTokenIsNoOp(t *testing.T) {
	registry := newPATConnectionRegistry()
	registry.revokeToken(999) // must not panic
}
