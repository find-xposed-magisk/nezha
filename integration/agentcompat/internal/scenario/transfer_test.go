//go:build linux

package scenario

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

func TestTransferEvidence_RejectsEmptyOrMismatchedSHA(t *testing.T) {
	t.Parallel()

	evidence := validTransferEvidence()
	evidence.UploadSHA256 = ""
	evidence.DownloadSHA256 = "different"

	err := evidence.Validate()

	require.ErrorIs(t, err, ErrTransferEvidenceHashMismatch)
	require.NotErrorIs(t, err, ErrTransferEvidenceSizeMismatch)
	require.NotErrorIs(t, err, ErrTransferEvidenceMeasurement)
	require.NotErrorIs(t, err, ErrTransferEvidenceHeapBudget)
	require.NotErrorIs(t, err, ErrTransferEvidenceContract)
}

func TestTransferEvidence_RejectsHeapBudgetBreach(t *testing.T) {
	t.Parallel()

	evidence := validTransferEvidence()
	evidence.RetainedHeapBytes = contract.TransferHeapBytes + 1

	err := evidence.Validate()

	require.ErrorIs(t, err, ErrTransferEvidenceHeapBudget)
	require.NotErrorIs(t, err, ErrTransferEvidenceSizeMismatch)
	require.NotErrorIs(t, err, ErrTransferEvidenceHashMismatch)
	require.NotErrorIs(t, err, ErrTransferEvidenceMeasurement)
	require.NotErrorIs(t, err, ErrTransferEvidenceContract)
}

func TestTransferEvidence_AcceptsExactNonemptyEqualHashes(t *testing.T) {
	t.Parallel()

	require.NoError(t, validTransferEvidence().Validate())
}

func TestTransferEvidence_RejectsMissingStreamingMeasurements(t *testing.T) {
	t.Parallel()

	evidence := validTransferEvidence()
	evidence.UploadChunks = 0
	evidence.DownloadChunks = 0
	evidence.UploadDuration = 0
	evidence.DownloadDuration = 0

	err := evidence.Validate()

	require.ErrorIs(t, err, ErrTransferEvidenceMeasurement)
	require.NotErrorIs(t, err, ErrTransferEvidenceSizeMismatch)
	require.NotErrorIs(t, err, ErrTransferEvidenceHashMismatch)
	require.NotErrorIs(t, err, ErrTransferEvidenceHeapBudget)
	require.NotErrorIs(t, err, ErrTransferEvidenceContract)
}

func TestTransferEvidence_ReportsAllTypedValidationErrors(t *testing.T) {
	t.Parallel()

	err := (TransferEvidence{}).Validate()

	require.ErrorIs(t, err, ErrTransferEvidenceSizeMismatch)
	require.ErrorIs(t, err, ErrTransferEvidenceHashMismatch)
	require.ErrorIs(t, err, ErrTransferEvidenceMeasurement)
	require.ErrorIs(t, err, ErrTransferEvidenceContract)
}

func TestTransferUploadEvidence_RejectsMissingMode(t *testing.T) {
	t.Parallel()

	digest := fixture.PayloadDigest{Bytes: contract.TransferBytes, SHA256: [32]byte{1}}
	evidence := transferPathEvidence{
		bytes: contract.TransferBytes, sha256: digest.Hex(), chunks: 1, duration: time.Nanosecond,
	}

	require.False(t, evidence.validUpload(digest))
	require.True(t, evidence.validDownload(digest))
}

func TestConfirmTransferQuiescence_RequiresDeadline(t *testing.T) {
	t.Parallel()

	_, err := confirmTransferQuiescence(context.Background(), transferResidueScope{})

	require.EqualError(t, err, "transfer quiescence requires a context deadline")
}

func validTransferEvidence() TransferEvidence {
	return TransferEvidence{
		WarmupUploadBytes:             transferWarmupBytes,
		WarmupDownloadBytes:           transferWarmupBytes,
		WarmupSHA256:                  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WarmupDuration:                time.Nanosecond,
		WarmupDeadlineRemaining:       time.Second,
		WarmupQuiescent:               true,
		UploadBytes:                   contract.TransferBytes,
		DownloadBytes:                 contract.TransferBytes,
		UploadSHA256:                  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DownloadSHA256:                "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		UploadChunks:                  1,
		DownloadChunks:                1,
		UploadDuration:                time.Nanosecond,
		DownloadDuration:              time.Nanosecond,
		RetainedHeapBytes:             contract.TransferHeapBytes,
		Mode:                          "0640",
		CreateDirs:                    true,
		UploadReplayRejected:          true,
		DownloadReplayRejected:        true,
		OversizeRejected:              true,
		OutsideRootSentinelsUnchanged: true,
	}
}
