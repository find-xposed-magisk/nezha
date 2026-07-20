//go:build linux && agentcompat

package scenario

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestTransferScenario_RealFlow(t *testing.T) {
	nezhaSource := os.Getenv("AGENTCOMPAT_NEZHA_SOURCE")
	agentSource := os.Getenv("AGENTCOMPAT_AGENT_SOURCE")
	if nezhaSource == "" || agentSource == "" {
		t.Skip("set AGENTCOMPAT_NEZHA_SOURCE and AGENTCOMPAT_AGENT_SOURCE")
	}
	evidenceDirectory := os.Getenv("AGENTCOMPAT_TRANSFER_EVIDENCE_DIR")
	if evidenceDirectory == "" {
		evidenceDirectory = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(evidenceDirectory, 0o700))
	paths, err := contract.NewPaths(nezhaSource, agentSource, evidenceDirectory)
	require.NoError(t, err)
	testContext, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	result, transferEvidence, err := (Transfer{}).RunWithEvidence(testContext, TransferInput{Paths: paths})

	logTransferAssertions(t, result)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.True(t, result.CleanupOK)
	require.NoError(t, transferEvidence.Validate())
	require.Equal(t, uint64(transferWarmupBytes), transferEvidence.WarmupUploadBytes)
	require.Equal(t, transferEvidence.WarmupUploadBytes, transferEvidence.WarmupDownloadBytes)
	require.NotEmpty(t, transferEvidence.WarmupSHA256)
	require.Positive(t, transferEvidence.WarmupDuration)
	require.Positive(t, transferEvidence.WarmupDeadlineRemaining)
	require.True(t, transferEvidence.WarmupQuiescent)
	require.Equal(t, uint64(104857600), transferEvidence.UploadBytes)
	require.Equal(t, uint64(104857600), transferEvidence.DownloadBytes)
	require.Equal(t, transferEvidence.UploadSHA256, transferEvidence.DownloadSHA256)
	require.NotEmpty(t, transferEvidence.UploadSHA256)
	require.Positive(t, transferEvidence.UploadChunks)
	require.Positive(t, transferEvidence.DownloadChunks)
	require.Positive(t, transferEvidence.UploadDuration)
	require.Positive(t, transferEvidence.DownloadDuration)
	require.LessOrEqual(t, transferEvidence.RetainedHeapBytes, uint64(16777216))
	require.Equal(t, "0640", transferEvidence.Mode)
	require.True(t, transferEvidence.CreateDirs)
	require.True(t, transferEvidence.UploadReplayRejected)
	require.True(t, transferEvidence.DownloadReplayRejected)
	require.True(t, transferEvidence.OversizeRejected)
	require.Zero(t, transferEvidence.AgentTempResidue)
	require.Zero(t, transferEvidence.DashboardSpoolResidue)
	require.True(t, transferEvidence.OutsideRootSentinelsUnchanged)
	requireTransferAssertions(t, result,
		"small real upload and download warm-up precedes event and deadline quiescence",
		"exact 100MiB upload has size mode SHA and create_dirs",
		"exact 100MiB download has equal nonempty SHA",
		"upload token replay is typed unauthorized",
		"download token replay is typed unauthorized",
		"100MiB plus one upload is typed too large",
		"retained live heap stays within 16MiB",
		"outside-root sentinels remain unchanged",
		"Dashboard spool and Agent temp residue are zero",
		"process listener and workspace cleanup completed",
	)

	fault, err := contract.NewFault("transfer-hash")
	require.NoError(t, err)
	faultResult, faultEvidence, faultErr := (Transfer{}).RunWithEvidence(testContext, TransferInput{Paths: paths, Fault: fault})
	logTransferAssertions(t, faultResult)
	require.ErrorIs(t, faultErr, ErrTransferHashFault)
	require.False(t, faultResult.Passed)
	require.True(t, faultResult.CleanupOK)
	require.Equal(t, uint64(transferWarmupBytes), faultEvidence.WarmupUploadBytes)
	require.Equal(t, faultEvidence.WarmupUploadBytes, faultEvidence.WarmupDownloadBytes)
	require.NotEmpty(t, faultEvidence.WarmupSHA256)
	require.Positive(t, faultEvidence.WarmupDuration)
	require.Positive(t, faultEvidence.WarmupDeadlineRemaining)
	require.True(t, faultEvidence.WarmupQuiescent)
	require.Zero(t, faultEvidence.AgentTempResidue)
	require.Zero(t, faultEvidence.DashboardSpoolResidue)
	require.True(t, faultEvidence.OutsideRootSentinelsUnchanged)
	requireTransferAssertions(t, faultResult,
		"small real upload and download warm-up precedes event and deadline quiescence",
		"transfer-hash rejects upload with typed 502",
		"transfer-hash leaves target absent",
		"outside-root sentinels remain unchanged",
		"Dashboard spool and Agent temp residue are zero",
		"process listener and workspace cleanup completed",
	)

	artifactPath := filepath.Join(evidenceDirectory, "transfer-real-process.json")
	artifact, err := json.MarshalIndent(struct {
		SuccessResult   Result           `json:"success_result"`
		SuccessEvidence TransferEvidence `json:"success_evidence"`
		FaultResult     Result           `json:"fault_result"`
		FaultEvidence   TransferEvidence `json:"fault_evidence"`
		FaultError      string           `json:"fault_error"`
	}{SuccessResult: result, SuccessEvidence: transferEvidence, FaultResult: faultResult, FaultEvidence: faultEvidence, FaultError: faultErr.Error()}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(artifactPath, append(artifact, '\n'), 0o600))
	readArtifact, err := os.ReadFile(artifactPath)
	require.NoError(t, err)
	var recorded struct {
		SuccessResult   Result           `json:"success_result"`
		SuccessEvidence TransferEvidence `json:"success_evidence"`
		FaultResult     Result           `json:"fault_result"`
		FaultEvidence   TransferEvidence `json:"fault_evidence"`
		FaultError      string           `json:"fault_error"`
	}
	require.NoError(t, json.Unmarshal(readArtifact, &recorded))
	require.Equal(t, result, recorded.SuccessResult)
	require.Equal(t, transferEvidence, recorded.SuccessEvidence)
	require.Equal(t, faultResult, recorded.FaultResult)
	require.Equal(t, faultEvidence, recorded.FaultEvidence)
	require.Equal(t, faultErr.Error(), recorded.FaultError)
	require.NoError(t, recorded.SuccessEvidence.Validate())
	t.Logf("transfer evidence artifact: %s", artifactPath)
}

func logTransferAssertions(t *testing.T, result Result) {
	t.Helper()
	for _, assertion := range result.Assertions {
		t.Logf("assertion=%q passed=%t details=%q", assertion.Name, assertion.Passed, assertion.Details)
	}
}

func requireTransferAssertions(t *testing.T, result Result, names ...string) {
	t.Helper()
	byName := make(map[string]Assertion, len(result.Assertions))
	for _, assertion := range result.Assertions {
		byName[assertion.Name] = assertion
	}
	for _, name := range names {
		assertion, exists := byName[name]
		require.True(t, exists, "missing assertion %q", name)
		require.True(t, assertion.Passed, "assertion %q failed: %s", name, assertion.Details)
	}
}
