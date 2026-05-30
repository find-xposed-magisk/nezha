package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
)

// H9 regression: CallAgent must consult mcpKillSwitchObserved before any
// side-effects. Without this gate, mcpEndpoint's EnableMCP read and
// CancelAllMCPInflight race against a fresh CallAgent that registers
// AFTER the cancel sweep, surviving the disabled state.
func TestCallAgent_RefusesWhenKillSwitchObserved(t *testing.T) {
	prevCheck := mcpKillSwitchObserver()
	SetMCPKillSwitchObserver(func() bool { return true })
	t.Cleanup(func() { SetMCPKillSwitchObserver(prevCheck) })

	_, err := CallAgent(context.Background(), 1, model.TaskTypeExec, struct{}{}, 50*time.Millisecond)
	if err != ErrMCPDisabled {
		t.Fatalf("CallAgent must short-circuit to ErrMCPDisabled when kill switch is observed, got %v", err)
	}
}

// The default hook must keep production behaviour disarmed so tests and
// unconfigured deployments do not short-circuit CallAgent.
func TestCallAgent_KillSwitchHookDefaultsToDisarmed(t *testing.T) {
	if mcpKillSwitchObserver() == nil {
		t.Fatal("mcpKillSwitchObserver must always return a non-nil probe so dashboard can wire it")
	}
	if mcpKillSwitchObserver()() {
		t.Fatal("default hook must return false so unconfigured dashboards / tests don't accidentally short-circuit CallAgent")
	}
}
