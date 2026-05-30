package controller

import (
	"errors"
	"testing"
)

// Review issue #3: when persisting the new EnableMCP value fails, updateConfig
// must NOT leave the dashboard in a half-disabled state where in-memory
// EnableMCP=false (new requests rejected) but the kill-switch cleanup
// (PurgeTransferEntries / RevokeStreamsForPurpose / CancelAllMCPInflight)
// never ran. applyEnableMCPTransition owns that invariant: on save failure it
// rolls the in-memory flag back to its previous value and runs no cleanup.

func TestApplyEnableMCPTransition_SaveFailureRollsBackAndSkipsCleanup(t *testing.T) {
	current := true
	cleanupRan := false

	setVal := func(v bool) { current = v }
	saveErr := errors.New("disk full")
	save := func() error { return saveErr }
	cleanup := func() { cleanupRan = true }

	err := applyEnableMCPTransition(true /*prev*/, false /*next*/, setVal, save, cleanup)

	if !errors.Is(err, saveErr) {
		t.Fatalf("expected the save error to propagate, got %v", err)
	}
	if current != true {
		t.Fatalf("in-memory EnableMCP must roll back to its previous value on save failure; got %v", current)
	}
	if cleanupRan {
		t.Fatal("kill-switch cleanup must NOT run when the new value was never persisted")
	}
}

func TestApplyEnableMCPTransition_DisableSuccessRunsCleanup(t *testing.T) {
	current := true
	cleanupRan := false

	setVal := func(v bool) { current = v }
	save := func() error { return nil }
	cleanup := func() { cleanupRan = true }

	if err := applyEnableMCPTransition(true, false, setVal, save, cleanup); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if current != false {
		t.Fatalf("EnableMCP must be committed to false after a successful save; got %v", current)
	}
	if !cleanupRan {
		t.Fatal("kill-switch cleanup must run when MCP transitions enabled->disabled and the save succeeds")
	}
}

func TestApplyEnableMCPTransition_EnableSuccessSkipsCleanup(t *testing.T) {
	current := false
	cleanupRan := false

	setVal := func(v bool) { current = v }
	save := func() error { return nil }
	cleanup := func() { cleanupRan = true }

	if err := applyEnableMCPTransition(false, true, setVal, save, cleanup); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if current != true {
		t.Fatalf("EnableMCP must be committed to true; got %v", current)
	}
	if cleanupRan {
		t.Fatal("cleanup must only run on the enabled->disabled transition, not when enabling")
	}
}
