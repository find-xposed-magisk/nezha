//go:build linux

package scenario

import (
	"errors"
	"fmt"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type ReconnectFixtureEvidence struct {
	Dashboard       dashboard.FixtureIdentity `json:"dashboard"`
	AgentRoot       string                    `json:"agent_root"`
	AgentConfigPath string                    `json:"agent_config_path"`
	AgentBinaryPath string                    `json:"agent_binary_path"`
}

type ReconnectRuntimeEvidence struct {
	DashboardBefore                   dashboard.RuntimeIdentity `json:"dashboard_before"`
	DashboardAfter                    dashboard.RuntimeIdentity `json:"dashboard_after"`
	AgentBefore                       agent.ProcessIdentity     `json:"agent_before"`
	AgentAfter                        agent.ProcessIdentity     `json:"agent_after"`
	StateGenerationBeforeAgentRestart uint64                    `json:"state_generation_before_agent_restart"`
	StateGenerationAfterAgentRestart  uint64                    `json:"state_generation_after_agent_restart"`
}

type ReconnectIdentityEvidence struct {
	ServerID                  uint64 `json:"server_id"`
	UUID                      string `json:"uuid"`
	DashboardConfigUnchanged  bool   `json:"dashboard_config_unchanged"`
	AgentConfigUnchanged      bool   `json:"agent_config_unchanged"`
	DashboardFixtureUnchanged bool   `json:"dashboard_fixture_unchanged"`
	ClientsRecreated          bool   `json:"clients_recreated"`
	BootstrapRecreated        bool   `json:"bootstrap_recreated"`
}

type ReconnectLifecycleEvidence struct {
	DisconnectAt                 time.Time                  `json:"disconnect_at"`
	ReconnectAt                  time.Time                  `json:"reconnect_at"`
	ReconnectInterval            time.Duration              `json:"reconnect_interval"`
	DashboardReceipts            []dashboard.MCPReceiptPair `json:"dashboard_receipts"`
	AgentReceipts                []dashboard.MCPReceiptPair `json:"agent_receipts"`
	StaleGenerationReceipts      int                        `json:"stale_generation_receipts"`
	DuplicateTaskIDs             int                        `json:"duplicate_task_ids"`
	LostResultIDs                int                        `json:"lost_result_ids"`
	OutsideRootSentinelUnchanged bool                       `json:"outside_root_sentinel_unchanged"`
}

type ReconnectEvidence struct {
	Fixture          ReconnectFixtureEvidence      `json:"fixture"`
	Runtime          ReconnectRuntimeEvidence      `json:"runtime"`
	Identity         ReconnectIdentityEvidence     `json:"identity"`
	Lifecycle        ReconnectLifecycleEvidence    `json:"lifecycle"`
	Observation      ReconnectObservation          `json:"observation"`
	AgentCleanup     processharness.CleanupReceipt `json:"agent_cleanup"`
	DashboardCleanup processharness.CleanupReceipt `json:"dashboard_cleanup"`
}

func (e ReconnectEvidence) Validate() error {
	var validationErr error
	validationErr = errors.Join(validationErr, e.Observation.Validate())
	if e.Runtime.DashboardAfter.Generation <= e.Runtime.DashboardBefore.Generation || e.Runtime.DashboardAfter.PID == e.Runtime.DashboardBefore.PID {
		validationErr = errors.Join(validationErr, errors.New("Dashboard runtime generation did not advance"))
	}
	if e.Runtime.AgentAfter.Generation <= e.Runtime.AgentBefore.Generation || e.Runtime.AgentAfter.PID == e.Runtime.AgentBefore.PID {
		validationErr = errors.Join(validationErr, errors.New("Agent runtime generation did not advance"))
	}
	if e.Runtime.StateGenerationAfterAgentRestart <= e.Runtime.StateGenerationBeforeAgentRestart {
		validationErr = errors.Join(validationErr, errors.New("Agent state stream generation did not advance"))
	}
	if !e.Identity.DashboardConfigUnchanged || !e.Identity.AgentConfigUnchanged || !e.Identity.DashboardFixtureUnchanged || !e.Identity.ClientsRecreated || !e.Identity.BootstrapRecreated {
		validationErr = errors.Join(validationErr, errors.New("reconnect identity evidence is incomplete"))
	}
	if e.Lifecycle.ReconnectInterval <= 0 || e.Lifecycle.StaleGenerationReceipts != 0 || e.Lifecycle.DuplicateTaskIDs != 0 || e.Lifecycle.LostResultIDs != 0 || !e.Lifecycle.OutsideRootSentinelUnchanged {
		validationErr = errors.Join(validationErr, fmt.Errorf("reconnect lifecycle evidence is invalid: interval=%s stale=%d duplicate=%d lost=%d sentinel=%t", e.Lifecycle.ReconnectInterval, e.Lifecycle.StaleGenerationReceipts, e.Lifecycle.DuplicateTaskIDs, e.Lifecycle.LostResultIDs, e.Lifecycle.OutsideRootSentinelUnchanged))
	}
	return validationErr
}
