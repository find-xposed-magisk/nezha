//go:build agentcompat

package rpc

import (
	"context"
	"testing"
	"time"
)

const agentCompatCapabilityTestTimeout = 5 * time.Second

func agentCompatCapabilityTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), agentCompatCapabilityTestTimeout)
	t.Cleanup(cancel)
	return ctx
}

func awaitAgentCompatCapabilitySignal(t *testing.T, signal <-chan struct{}, failureMessage string) {
	t.Helper()
	select {
	case <-signal:
	case <-agentCompatCapabilityTestContext(t).Done():
		t.Fatal(failureMessage)
	}
}

func receiveAgentCompatCapabilityError(t *testing.T, result <-chan error, failureMessage string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-agentCompatCapabilityTestContext(t).Done():
		t.Fatal(failureMessage)
		return nil
	}
}
