package evidence

import (
	"errors"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type transferEvidence struct {
	WarmupUploadBytes             uint64        `json:"warmup_upload_bytes"`
	WarmupDownloadBytes           uint64        `json:"warmup_download_bytes"`
	WarmupSHA256                  string        `json:"warmup_sha256"`
	WarmupDuration                time.Duration `json:"warmup_duration"`
	WarmupDeadlineRemaining       time.Duration `json:"warmup_deadline_remaining"`
	WarmupQuiescent               bool          `json:"warmup_quiescent"`
	UploadBytes                   uint64        `json:"upload_bytes"`
	DownloadBytes                 uint64        `json:"download_bytes"`
	UploadSHA256                  string        `json:"upload_sha256"`
	DownloadSHA256                string        `json:"download_sha256"`
	UploadChunks                  uint64        `json:"upload_chunks"`
	DownloadChunks                uint64        `json:"download_chunks"`
	UploadDuration                time.Duration `json:"upload_duration"`
	DownloadDuration              time.Duration `json:"download_duration"`
	RetainedHeapBytes             uint64        `json:"retained_heap_bytes"`
	Mode                          string        `json:"mode"`
	CreateDirs                    bool          `json:"create_dirs"`
	UploadReplayRejected          bool          `json:"upload_replay_rejected"`
	DownloadReplayRejected        bool          `json:"download_replay_rejected"`
	OversizeRejected              bool          `json:"oversize_rejected"`
	AgentTempResidue              int           `json:"agent_temp_residue"`
	DashboardSpoolResidue         int           `json:"dashboard_spool_residue"`
	OutsideRootSentinelsUnchanged bool          `json:"outside_root_sentinels_unchanged"`
}

type transferArtifact struct {
	Scenario  string           `json:"scenario"`
	Fault     string           `json:"fault,omitempty"`
	Passed    bool             `json:"passed"`
	CleanupOK bool             `json:"cleanup_ok"`
	Error     string           `json:"error,omitempty"`
	Evidence  transferEvidence `json:"evidence"`
}

type reconnectObservation struct {
	ServerID       uint64
	UUID           string
	OldGeneration  uint64
	NewGeneration  uint64
	DisconnectAt   time.Time
	ReconnectAt    time.Time
	TaskIDs        []uint64
	ResultIDs      []uint64
	PostReconnect  bool
	AgentRestarted bool
}

type listenerIdentity struct {
	Address string
	Inode   uint64
}

type fixtureIdentity struct {
	WorkspaceRoot string
	ConfigPath    string
	DatabasePath  string
	BinaryPath    string
	HTTP          listenerIdentity
	Receipt       listenerIdentity
	HTTPS         listenerIdentity
}

type runtimeIdentity struct {
	Generation     uint64
	PID            int
	ProcessGroupID int
}

type receiptEvent struct {
	Sequence            uint64 `json:"sequence"`
	DashboardGeneration uint64 `json:"dashboard_generation"`
	GateGeneration      uint64 `json:"gate_generation"`
	ServerID            uint64 `json:"server_id"`
	TaskID              uint64 `json:"task_id"`
	TaskType            uint64 `json:"task_type"`
	Kind                string `json:"kind"`
}

type receiptPair struct {
	Task   receiptEvent `json:"task"`
	Result receiptEvent `json:"result"`
}

type cleanupRecord struct {
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	Forced bool   `json:"forced"`
	Error  string `json:"error,omitempty"`
}

type cleanupReceipt struct {
	Passed    bool            `json:"passed"`
	Forced    bool            `json:"forced"`
	Processes []cleanupRecord `json:"processes"`
}

type reconnectEvidence struct {
	Fixture struct {
		Dashboard       fixtureIdentity `json:"dashboard"`
		AgentRoot       string          `json:"agent_root"`
		AgentConfigPath string          `json:"agent_config_path"`
		AgentBinaryPath string          `json:"agent_binary_path"`
	} `json:"fixture"`
	Runtime struct {
		DashboardBefore                   runtimeIdentity `json:"dashboard_before"`
		DashboardAfter                    runtimeIdentity `json:"dashboard_after"`
		AgentBefore                       runtimeIdentity `json:"agent_before"`
		AgentAfter                        runtimeIdentity `json:"agent_after"`
		StateGenerationBeforeAgentRestart uint64          `json:"state_generation_before_agent_restart"`
		StateGenerationAfterAgentRestart  uint64          `json:"state_generation_after_agent_restart"`
	} `json:"runtime"`
	Identity struct {
		ServerID                  uint64 `json:"server_id"`
		UUID                      string `json:"uuid"`
		DashboardConfigUnchanged  bool   `json:"dashboard_config_unchanged"`
		AgentConfigUnchanged      bool   `json:"agent_config_unchanged"`
		DashboardFixtureUnchanged bool   `json:"dashboard_fixture_unchanged"`
		ClientsRecreated          bool   `json:"clients_recreated"`
		BootstrapRecreated        bool   `json:"bootstrap_recreated"`
	} `json:"identity"`
	Lifecycle struct {
		DisconnectAt                 time.Time     `json:"disconnect_at"`
		ReconnectAt                  time.Time     `json:"reconnect_at"`
		ReconnectInterval            time.Duration `json:"reconnect_interval"`
		DashboardReceipts            []receiptPair `json:"dashboard_receipts"`
		AgentReceipts                []receiptPair `json:"agent_receipts"`
		StaleGenerationReceipts      int           `json:"stale_generation_receipts"`
		DuplicateTaskIDs             int           `json:"duplicate_task_ids"`
		LostResultIDs                int           `json:"lost_result_ids"`
		OutsideRootSentinelUnchanged bool          `json:"outside_root_sentinel_unchanged"`
	} `json:"lifecycle"`
	Observation      reconnectObservation `json:"observation"`
	AgentCleanup     cleanupReceipt       `json:"agent_cleanup"`
	DashboardCleanup cleanupReceipt       `json:"dashboard_cleanup"`
}

type reconnectArtifact struct {
	Scenario  string            `json:"scenario"`
	Fault     string            `json:"fault,omitempty"`
	Passed    bool              `json:"passed"`
	CleanupOK bool              `json:"cleanup_ok"`
	Error     string            `json:"error,omitempty"`
	Evidence  reconnectEvidence `json:"evidence"`
}

func validateTransferArtifact(metadata Metadata, result ScenarioResult, artifact transferArtifact) error {
	if err := validateArtifactHeader(metadata, result, artifact.Scenario, artifact.Fault, artifact.Passed, artifact.CleanupOK, artifact.Error); err != nil {
		return err
	}
	switch artifact.Fault {
	case "":
		if !artifact.Passed {
			return errors.New("transfer success artifact reports failure")
		}
		if err := validateTransferSuccess(artifact.Evidence); err != nil {
			return err
		}
	case contract.FaultTransferHash:
		if artifact.Passed || !strings.Contains(artifact.Error, "injected hash mismatch") {
			return errors.New("transfer-hash artifact does not identify the injected failure")
		}
		evidence := artifact.Evidence
		if evidence.WarmupUploadBytes != 65536 || evidence.WarmupDownloadBytes != 65536 || evidence.WarmupSHA256 == "" || evidence.WarmupDuration <= 0 || evidence.WarmupDeadlineRemaining <= 0 || !evidence.WarmupQuiescent {
			return errors.New("transfer-hash artifact omitted warmup evidence")
		}
		if evidence.UploadBytes != 0 || evidence.DownloadBytes != 0 || evidence.UploadSHA256 != "" || evidence.DownloadSHA256 != "" || evidence.UploadChunks != 0 || evidence.DownloadChunks != 0 || evidence.UploadDuration != 0 || evidence.DownloadDuration != 0 || evidence.RetainedHeapBytes != 0 || evidence.Mode != "" || evidence.CreateDirs || evidence.UploadReplayRejected || evidence.DownloadReplayRejected || evidence.OversizeRejected {
			return errors.New("transfer-hash artifact presents stale success evidence")
		}
		if evidence.AgentTempResidue != 0 || evidence.DashboardSpoolResidue != 0 || !evidence.OutsideRootSentinelsUnchanged {
			return errors.New("transfer-hash artifact cleanup or sentinel evidence failed")
		}
	default:
		return errors.New("transfer artifact has unsupported fault")
	}
	return nil
}

func validateArtifactHeader(metadata Metadata, result ScenarioResult, scenarioName, fault string, passed, cleanupOK bool, errorText string) error {
	if scenarioName != result.Name || scenarioName != metadata.Scenarios[0] || fault != metadata.Fault || passed != result.Passed || !cleanupOK || errorText != result.Error {
		return errors.New("dedicated artifact does not agree with metadata, results, or cleanup")
	}
	if passed && errorText != "" || !passed && errorText == "" {
		return errors.New("dedicated artifact pass/error state is inconsistent")
	}
	return nil
}

func validateTransferSuccess(evidence transferEvidence) error {
	if evidence.WarmupUploadBytes != 65536 || evidence.WarmupDownloadBytes != 65536 || evidence.WarmupSHA256 == "" || evidence.WarmupDuration <= 0 || evidence.WarmupDeadlineRemaining <= 0 || !evidence.WarmupQuiescent {
		return errors.New("transfer warmup evidence is invalid")
	}
	if evidence.UploadBytes != contract.TransferBytes || evidence.DownloadBytes != contract.TransferBytes || evidence.UploadSHA256 == "" || !strings.EqualFold(evidence.UploadSHA256, evidence.DownloadSHA256) {
		return errors.New("transfer byte counts or hashes are invalid")
	}
	if evidence.UploadChunks == 0 || evidence.DownloadChunks == 0 || evidence.UploadDuration <= 0 || evidence.DownloadDuration <= 0 || evidence.RetainedHeapBytes > contract.TransferHeapBytes {
		return errors.New("transfer measurement evidence is invalid")
	}
	if evidence.Mode != "0640" || !evidence.CreateDirs || !evidence.UploadReplayRejected || !evidence.DownloadReplayRejected || !evidence.OversizeRejected || evidence.AgentTempResidue != 0 || evidence.DashboardSpoolResidue != 0 || !evidence.OutsideRootSentinelsUnchanged {
		return errors.New("transfer contract evidence is invalid")
	}
	return nil
}
