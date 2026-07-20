//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

type transferExecution struct {
	client       *client.Client
	serverID     uint64
	root         fixture.AgentRoot
	residueScope transferResidueScope
	sentinels    transferSentinels
}

func (execution transferExecution) run(ctx context.Context, assertions *AssertionSet, fault contract.Fault) (TransferEvidence, error) {
	payload, err := fixture.NewPayload(contract.DefaultSeed, contract.TransferBytes)
	if err != nil {
		return TransferEvidence{}, err
	}
	stableDigest, err := fixture.VerifyPayload(payload.Reader(), contract.TransferBytes)
	if err != nil {
		return TransferEvidence{}, err
	}
	warmupEvidence, err := execution.runWarmup(ctx)
	if err != nil {
		return TransferEvidence{}, fmt.Errorf("transfer warm-up: %w", err)
	}
	quiescenceDeadline, err := confirmTransferQuiescence(ctx, execution.residueScope)
	if err != nil {
		return TransferEvidence{}, err
	}
	warmupEvidence.deadline = quiescenceDeadline
	assertions.Record("small real upload and download warm-up precedes event and deadline quiescence", warmupEvidence.valid(), fmt.Sprintf("completion_event=download_response upload_bytes=%d download_bytes=%d sha256=%s duration=%s deadline_remaining=%s", warmupEvidence.uploadBytes, warmupEvidence.downloadBytes, warmupEvidence.sha256, warmupEvidence.duration, warmupEvidence.deadline))

	uploadPath, err := execution.root.Path("measured/nested/upload.bin")
	if err != nil {
		return TransferEvidence{}, err
	}
	heapProbe := fixture.NewRetainedHeapProbe()
	uploadEvidence, uploadURL, uploadErr := execution.upload(ctx, uploadPath, payload, stableDigest, fault)
	if fault.String() == "transfer-hash" {
		return execution.finishHashFault(ctx, assertions, uploadPath, warmupEvidence, uploadErr)
	}
	if uploadErr != nil {
		return TransferEvidence{}, uploadErr
	}
	assertions.Record("exact 100MiB upload has size mode SHA and create_dirs", uploadEvidence.validUpload(stableDigest), uploadEvidence.details())

	downloadEvidence, downloadURL, downloadErr := execution.download(ctx, uploadPath)
	if downloadErr != nil {
		return TransferEvidence{}, downloadErr
	}
	assertions.Record("exact 100MiB download has equal nonempty SHA", downloadEvidence.validDownload(stableDigest), downloadEvidence.details())
	retainedHeapBytes := heapProbe.RetainedBytes()

	uploadReplayErr := execution.replayUpload(ctx, uploadURL)
	uploadReplayRejected := isTransferHTTPError(uploadReplayErr, 401, "already-used")
	assertions.Record("upload token replay is typed unauthorized", uploadReplayRejected, errorText(uploadReplayErr))

	downloadReplayErr := execution.replayDownload(ctx, downloadURL)
	downloadReplayRejected := isTransferHTTPError(downloadReplayErr, 401, "")
	assertions.Record("download token replay is typed unauthorized", downloadReplayRejected, errorText(downloadReplayErr))
	oversizeErr := execution.probeOversize(ctx)
	oversizeRejected := isTransferHTTPError(oversizeErr, 413, "transfer cap")
	assertions.Record("100MiB plus one upload is typed too large", oversizeRejected, errorText(oversizeErr))

	if _, err := confirmTransferQuiescence(ctx, execution.residueScope); err != nil {
		return TransferEvidence{}, err
	}
	residue, err := transferResidue(execution.residueScope)
	if err != nil {
		return TransferEvidence{}, err
	}
	agentResidue, dashboardResidue := countTransferResidue(residue)
	assertions.Record("Dashboard spool and Agent temp residue are zero", agentResidue == 0 && dashboardResidue == 0, fmt.Sprintf("agent_temp=%d dashboard_spool=%d", agentResidue, dashboardResidue))
	sentinelsUnchanged, sentinelErr := execution.sentinels.unchanged()
	assertions.Record("outside-root sentinels remain unchanged", sentinelErr == nil && sentinelsUnchanged, errorText(sentinelErr))

	evidence := TransferEvidence{
		WarmupUploadBytes: warmupEvidence.uploadBytes, WarmupDownloadBytes: warmupEvidence.downloadBytes,
		WarmupSHA256: warmupEvidence.sha256, WarmupDuration: warmupEvidence.duration,
		WarmupDeadlineRemaining: warmupEvidence.deadline, WarmupQuiescent: true,
		UploadBytes: uploadEvidence.bytes, DownloadBytes: downloadEvidence.bytes,
		UploadSHA256: uploadEvidence.sha256, DownloadSHA256: downloadEvidence.sha256,
		UploadChunks: uploadEvidence.chunks, DownloadChunks: downloadEvidence.chunks,
		UploadDuration: uploadEvidence.duration, DownloadDuration: downloadEvidence.duration,
		RetainedHeapBytes: retainedHeapBytes, Mode: "0640", CreateDirs: true,
		UploadReplayRejected: uploadReplayRejected, DownloadReplayRejected: downloadReplayRejected, OversizeRejected: oversizeRejected,
		AgentTempResidue: agentResidue, DashboardSpoolResidue: dashboardResidue,
		OutsideRootSentinelsUnchanged: sentinelsUnchanged,
	}
	heapErr := evidence.Validate()
	assertions.Record("retained live heap stays within 16MiB", !errors.Is(heapErr, ErrTransferEvidenceHeapBudget), fmt.Sprintf("retained_heap_bytes=%d", evidence.RetainedHeapBytes))
	if err := evidence.Validate(); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func (execution transferExecution) finishHashFault(ctx context.Context, assertions *AssertionSet, uploadPath fixture.AgentPath, warmup transferWarmupEvidence, uploadErr error) (TransferEvidence, error) {
	assertions.Record("transfer-hash rejects upload with typed 502", isTransferHTTPError(uploadErr, 502, "sha256 mismatch"), errorText(uploadErr))
	_, statErr := os.Stat(uploadPath.String())
	assertions.Record("transfer-hash leaves target absent", errors.Is(statErr, os.ErrNotExist), errorText(statErr))
	if _, err := confirmTransferQuiescence(ctx, execution.residueScope); err != nil {
		return TransferEvidence{}, err
	}
	residue, err := transferResidue(execution.residueScope)
	if err != nil {
		return TransferEvidence{}, err
	}
	agentResidue, dashboardResidue := countTransferResidue(residue)
	assertions.Record("Dashboard spool and Agent temp residue are zero", agentResidue == 0 && dashboardResidue == 0, fmt.Sprintf("agent_temp=%d dashboard_spool=%d", agentResidue, dashboardResidue))
	unchanged, sentinelErr := execution.sentinels.unchanged()
	assertions.Record("outside-root sentinels remain unchanged", sentinelErr == nil && unchanged, errorText(sentinelErr))
	return TransferEvidence{
		WarmupUploadBytes: warmup.uploadBytes, WarmupDownloadBytes: warmup.downloadBytes,
		WarmupSHA256: warmup.sha256, WarmupDuration: warmup.duration,
		WarmupDeadlineRemaining: warmup.deadline, WarmupQuiescent: true,
		AgentTempResidue: agentResidue, DashboardSpoolResidue: dashboardResidue, OutsideRootSentinelsUnchanged: unchanged,
	}, ErrTransferHashFault
}

func isTransferHTTPError(err error, status int, text string) bool {
	var httpError *client.HTTPError
	return errors.As(err, &httpError) && httpError.StatusCode == status && (text == "" || strings.Contains(httpError.Message, text))
}

type transferPathEvidence struct {
	bytes    uint64
	sha256   string
	chunks   uint64
	duration time.Duration
	mode     os.FileMode
}

func (evidence transferPathEvidence) details() string {
	return fmt.Sprintf("bytes=%d sha256=%s chunks=%d duration=%s mode=%04o", evidence.bytes, evidence.sha256, evidence.chunks, evidence.duration, evidence.mode.Perm())
}

func (evidence transferPathEvidence) validUpload(want fixture.PayloadDigest) bool {
	return evidence.validDownload(want) && evidence.mode.Perm() == 0o640
}

func (evidence transferPathEvidence) validDownload(want fixture.PayloadDigest) bool {
	return evidence.bytes == contract.TransferBytes && evidence.sha256 != "" && evidence.sha256 == want.Hex()
}
