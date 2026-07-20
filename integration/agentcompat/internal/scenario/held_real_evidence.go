//go:build linux && agentcompat

package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type heldRealEvidence struct {
	Kind                    string `json:"kind"`
	BaselineCount           int    `json:"baseline_count"`
	LiveCount               int    `json:"live_count"`
	ClosedCount             int    `json:"closed_count"`
	ExactIDPresent          bool   `json:"exact_id_present"`
	ExactIDAbsent           bool   `json:"exact_id_absent"`
	ProtocolProved          bool   `json:"protocol_proved"`
	SensitiveHeadersPresent bool   `json:"sensitive_headers_present"`
	DashboardPIDUnchanged   bool   `json:"dashboard_pid_unchanged"`
	AgentPIDUnchanged       bool   `json:"agent_pid_unchanged"`
	CleanupOK               bool   `json:"cleanup_ok"`
}

type heldRealCleanup struct {
	Agent             processharness.CleanupReceipt
	Dashboard         processharness.CleanupReceipt
	SessionClosed     bool
	ExactStreamGone   bool
	OwnedResourceGone bool
	AgentPIDGone      bool
	DashboardPIDGone  bool
}

func heldRealArtifactKinds() []string {
	return []string{"terminal", "file-manager", "nat"}
}

func heldRealCleanupOK(cleanup heldRealCleanup) bool {
	return cleanup.Agent.Passed && cleanup.Dashboard.Passed && cleanup.SessionClosed && cleanup.ExactStreamGone && cleanup.OwnedResourceGone && cleanup.AgentPIDGone && cleanup.DashboardPIDGone
}

func heldRealArtifactKeys() []string {
	return []string{
		"kind", "baseline_count", "live_count", "closed_count", "exact_id_present", "exact_id_absent",
		"protocol_proved", "sensitive_headers_present", "dashboard_pid_unchanged", "agent_pid_unchanged", "cleanup_ok",
	}
}

func writeHeldRealEvidence(kind string, evidence heldRealEvidence) error {
	if !slices.Contains(heldRealArtifactKinds(), kind) || evidence.Kind != kind {
		return fmt.Errorf("held real evidence kind is invalid")
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("encode held real evidence: %w", err)
	}
	root := "/tmp/nezha-held-real-sessions"
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create held real evidence directory: %w", err)
	}
	path := filepath.Join(root, kind+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write held real evidence: %w", err)
	}
	return nil
}
