//go:build linux

package scenario

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

var (
	ErrTransferEvidenceSizeMismatch = errors.New("transfer evidence size mismatch")
	ErrTransferEvidenceHashMismatch = errors.New("transfer evidence hash mismatch")
	ErrTransferEvidenceHeapBudget   = errors.New("transfer evidence retained heap budget exceeded")
	ErrTransferEvidenceMeasurement  = errors.New("transfer evidence measurement missing")
	ErrTransferEvidenceContract     = errors.New("transfer evidence contract assertion failed")
)

type TransferEvidence struct {
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

func (e TransferEvidence) Validate() error {
	var validationErr error
	if e.WarmupUploadBytes != transferWarmupBytes || e.WarmupDownloadBytes != transferWarmupBytes || e.WarmupSHA256 == "" || e.WarmupDuration <= 0 || e.WarmupDeadlineRemaining <= 0 || !e.WarmupQuiescent {
		validationErr = errors.Join(validationErr, fmt.Errorf("%w: warmup_upload=%d warmup_download=%d warmup_sha=%q warmup_duration=%s warmup_deadline=%s warmup_quiescent=%t", ErrTransferEvidenceMeasurement, e.WarmupUploadBytes, e.WarmupDownloadBytes, e.WarmupSHA256, e.WarmupDuration, e.WarmupDeadlineRemaining, e.WarmupQuiescent))
	}
	if e.UploadBytes != contract.TransferBytes || e.DownloadBytes != contract.TransferBytes {
		validationErr = errors.Join(validationErr, fmt.Errorf("%w: upload=%d download=%d want=%d", ErrTransferEvidenceSizeMismatch, e.UploadBytes, e.DownloadBytes, contract.TransferBytes))
	}
	if e.UploadSHA256 == "" || e.DownloadSHA256 == "" || !strings.EqualFold(e.UploadSHA256, e.DownloadSHA256) {
		validationErr = errors.Join(validationErr, fmt.Errorf("%w: upload=%q download=%q", ErrTransferEvidenceHashMismatch, e.UploadSHA256, e.DownloadSHA256))
	}
	if e.UploadChunks == 0 || e.DownloadChunks == 0 || e.UploadDuration <= 0 || e.DownloadDuration <= 0 {
		validationErr = errors.Join(validationErr, fmt.Errorf("%w: upload_chunks=%d download_chunks=%d upload_duration=%s download_duration=%s", ErrTransferEvidenceMeasurement, e.UploadChunks, e.DownloadChunks, e.UploadDuration, e.DownloadDuration))
	}
	if e.RetainedHeapBytes > contract.TransferHeapBytes {
		validationErr = errors.Join(validationErr, fmt.Errorf("%w: retained=%d limit=%d", ErrTransferEvidenceHeapBudget, e.RetainedHeapBytes, contract.TransferHeapBytes))
	}
	if e.Mode != "0640" || !e.CreateDirs || !e.UploadReplayRejected || !e.DownloadReplayRejected || !e.OversizeRejected || e.AgentTempResidue != 0 || e.DashboardSpoolResidue != 0 || !e.OutsideRootSentinelsUnchanged {
		validationErr = errors.Join(validationErr, fmt.Errorf("%w: mode=%q create_dirs=%t upload_replay=%t download_replay=%t oversize=%t agent_temp=%d dashboard_spool=%d sentinels=%t", ErrTransferEvidenceContract, e.Mode, e.CreateDirs, e.UploadReplayRejected, e.DownloadReplayRejected, e.OversizeRejected, e.AgentTempResidue, e.DashboardSpoolResidue, e.OutsideRootSentinelsUnchanged))
	}
	return validationErr
}
