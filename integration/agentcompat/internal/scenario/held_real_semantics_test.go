//go:build linux && agentcompat

package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

func TestHeldRealArtifactKindsIncludeEveryRealSessionKind(t *testing.T) {
	require.ElementsMatch(t, []string{"terminal", "file-manager", "nat"}, heldRealArtifactKinds())
}

func TestHeldRealCleanupOKRejectsReceiptErrorAndRunningPID(t *testing.T) {
	passedReceipt := processharness.NewCleanupReceipt([]processharness.CleanupRecord{{Name: "process", PID: 1}})
	failedReceipt := processharness.NewCleanupReceipt([]processharness.CleanupRecord{{Name: "process", PID: 1, Error: "cleanup failed"}})

	complete := heldRealCleanup{Agent: passedReceipt, Dashboard: passedReceipt, SessionClosed: true, ExactStreamGone: true, OwnedResourceGone: true, AgentPIDGone: true, DashboardPIDGone: true}
	require.True(t, heldRealCleanupOK(complete))
	complete.Agent = failedReceipt
	require.False(t, heldRealCleanupOK(complete))
	complete.Agent = passedReceipt
	complete.AgentPIDGone = false
	require.False(t, heldRealCleanupOK(complete))
}

func TestHeldRealNATProfileQueryPropagatesRESTError(t *testing.T) {
	wantErr := errors.New("query failed")
	present, err := heldRealNATProfilePresentWithQuery(context.Background(), 9, func(context.Context) ([]heldRealNATProfile, error) {
		return nil, wantErr
	})

	require.ErrorIs(t, err, wantErr)
	require.False(t, present)
}

func TestHeldRealEvidenceUsesOnlyRedactedSchema(t *testing.T) {
	data, err := json.Marshal(heldRealEvidence{Kind: "terminal", SensitiveHeadersPresent: false})
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &fields))
	for field := range fields {
		require.Contains(t, heldRealArtifactKeys(), field)
	}
	require.NotContains(t, strings.ToLower(string(data)), "stream")
	require.NotContains(t, strings.ToLower(string(data)), "token")
	require.NotContains(t, strings.ToLower(string(data)), "authorization")
	require.Error(t, writeHeldRealEvidence("unknown", heldRealEvidence{Kind: "unknown"}))
}
