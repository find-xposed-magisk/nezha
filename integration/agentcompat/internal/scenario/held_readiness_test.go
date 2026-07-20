//go:build linux

package scenario

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
)

func TestHeldReadinessValidation_AcceptsCompleteEvidenceForActualAgent(t *testing.T) {
	// Given
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000301")
	readiness := completeHeldReadiness(agentInstance.UUID())

	// When
	err := validateHeldReadiness(agentInstance, readiness)

	// Then
	require.NoError(t, err)
}

func TestHeldReadinessValidation_RejectsEachInvalidDimensionWithExactError(t *testing.T) {
	// Given
	agentInstance := newHeldReadinessTestAgent(t, "00000000-0000-0000-0000-000000000302")
	valid := completeHeldReadiness(agentInstance.UUID())
	allSentinels := []error{
		ErrHeldReadinessServerID,
		ErrHeldReadinessUUID,
		ErrHeldReadinessAgentMismatch,
		ErrHeldReadinessVersion,
		ErrHeldReadinessOnline,
		ErrHeldReadinessVersionObserved,
		ErrHeldReadinessRequestTaskEstablished,
		ErrHeldReadinessStateReceiptObserved,
	}
	tests := []struct {
		name      string
		field     string
		wantError error
		mutate    func(*agent.Readiness)
	}{
		{name: "zero server ID", field: "server_id", wantError: ErrHeldReadinessServerID, mutate: func(readiness *agent.Readiness) { readiness.ServerID = 0 }},
		{name: "empty UUID", field: "uuid", wantError: ErrHeldReadinessUUID, mutate: func(readiness *agent.Readiness) { readiness.UUID = "" }},
		{name: "agent UUID mismatch", field: "uuid", wantError: ErrHeldReadinessAgentMismatch, mutate: func(readiness *agent.Readiness) { readiness.UUID = "00000000-0000-0000-0000-000000000399" }},
		{name: "empty version", field: "version", wantError: ErrHeldReadinessVersion, mutate: func(readiness *agent.Readiness) { readiness.Version = "" }},
		{name: "offline", field: "online", wantError: ErrHeldReadinessOnline, mutate: func(readiness *agent.Readiness) { readiness.Online = false }},
		{name: "version not observed", field: "version_observed", wantError: ErrHeldReadinessVersionObserved, mutate: func(readiness *agent.Readiness) { readiness.VersionObserved = false }},
		{name: "request task not established", field: "request_task_established", wantError: ErrHeldReadinessRequestTaskEstablished, mutate: func(readiness *agent.Readiness) { readiness.RequestTaskEstablished = false }},
		{name: "state receipt not observed", field: "state_receipt_observed", wantError: ErrHeldReadinessStateReceiptObserved, mutate: func(readiness *agent.Readiness) { readiness.StateReceiptObserved = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readiness := valid
			test.mutate(&readiness)

			// When
			err := validateHeldReadiness(agentInstance, readiness)

			// Then
			require.ErrorIs(t, err, ErrInvalidHeldReadiness)
			require.ErrorIs(t, err, test.wantError)
			var validationError *HeldReadinessValidationError
			require.ErrorAs(t, err, &validationError)
			require.Equal(t, test.field, validationError.Field)
			require.NotContains(t, err.Error(), valid.UUID)
			require.NotContains(t, err.Error(), valid.Version)
			for _, sentinel := range allSentinels {
				if sentinel != test.wantError {
					require.NotErrorIs(t, err, sentinel)
				}
			}
		})
	}
}

func completeHeldReadiness(uuid string) agent.Readiness {
	return agent.Readiness{
		ServerID:               301,
		UUID:                   uuid,
		Version:                "v2.1.0",
		Online:                 true,
		VersionObserved:        true,
		RequestTaskEstablished: true,
		StateReceiptObserved:   true,
	}
}

func newHeldReadinessTestAgent(t *testing.T, uuid string) *agent.Agent {
	t.Helper()
	sourceDirectory := t.TempDir()
	mainDirectory := filepath.Join(sourceDirectory, "cmd", "agent")
	monitorDirectory := filepath.Join(sourceDirectory, "pkg", "monitor")
	require.NoError(t, os.MkdirAll(mainDirectory, 0o700))
	require.NoError(t, os.MkdirAll(monitorDirectory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDirectory, "go.mod"), []byte("module github.com/nezhahq/agent\n\ngo 1.26.3\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(monitorDirectory, "version.go"), []byte("package monitor\n\nvar Version = \"test\"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(mainDirectory, "main.go"), []byte(`package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/nezhahq/agent/pkg/monitor"
)

func main() {
	_ = monitor.Version
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
}
`), 0o600))

	agentInstance, err := agent.Start(t.Context(), agent.AgentStartConfig{
		SourceDir: sourceDirectory,
		Endpoint:  "127.0.0.1:1",
		UUID:      uuid,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, agentInstance.Stop(cleanupContext))
	})
	return agentInstance
}
