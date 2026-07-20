//go:build linux && agentcompat

package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestStressArtifactRejectsUnknownAndRuntimeFields(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	root := t.TempDir()
	valid := validStressArtifact(t, profile)
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	for _, mutation := range []string{
		`{"unknown":true}`,
		`{"pid":123}`,
		`{"operation_id":"op"}`,
	} {
		path := filepath.Join(root, "stress.json")
		require.NoError(t, os.WriteFile(path, append(data[:len(data)-1], []byte(","+mutation[1:])...), 0o600))
		_, err = readStressEvidence(root)
		require.ErrorIs(t, err, ErrStressArtifactInvalid)
	}
}

func TestStressArtifactRejectsTrailingDataAndWrongSeed(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	root := t.TempDir()
	valid := validStressArtifact(t, profile)
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "stress.json"), append(data, []byte("\n{}")...), 0o600))
	_, err = readStressEvidence(root)
	require.ErrorIs(t, err, ErrStressArtifactInvalid)

	valid.Seed = "4e5a4842"
	data, err = json.Marshal(valid)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "stress.json"), data, 0o600))
	_, err = readStressEvidence(root)
	require.ErrorIs(t, err, ErrStressArtifactInvalid)
}

func TestStressCleanupMutationsCannotPublishSuccess(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	value := validStressEvidence(t, profile)
	for _, mutate := range []func(*StressEvidence){
		func(evidence *StressEvidence) { evidence.Cleanup.FailedReceiptCount = 1 },
		func(evidence *StressEvidence) { evidence.Cleanup.ForcedCleanupCount = 1 },
		func(evidence *StressEvidence) { evidence.Cleanup.ProcessResidue = 1 },
		func(evidence *StressEvidence) { evidence.Cleanup.ProcessGroupResidue = 1 },
		func(evidence *StressEvidence) { evidence.Cleanup.WorkspaceResidue = 1 },
	} {
		candidate := value
		mutate(&candidate)
		_, err := newStressArtifact(candidate)
		require.Error(t, err)
	}
}

func TestStressArtifactAggregatesExactlyNineResourceEvaluations(t *testing.T) {
	profile, err := contract.ProfileByName(string(contract.ProfilePRFull))
	require.NoError(t, err)
	value := validStressEvidence(t, profile)
	artifact, err := newStressArtifact(value)
	require.NoError(t, err)
	require.Equal(t, 0, artifact.ResourceDrift)
	require.True(t, artifact.RSSBounded)

	value.Iterations[0].Resources = value.Iterations[0].Resources[:8]
	_, err = newStressArtifact(value)
	require.ErrorIs(t, err, ErrStressArtifactInvalid)
}

func validStressArtifact(t *testing.T, profile contract.Profile) stressArtifact {
	t.Helper()
	return stressArtifact{
		Version: 1, Profile: string(profile.Name()), Seed: "4e5a4841",
		RoundCount: 4, OperationCount: 64, SessionCount: 12, WarmupCount: 8,
		ResourceSummaryCount: 9, ResourceSampleCount: 5, ResourceIntervalMilliseconds: 250,
		Quotas:          StressQuotaEvidence{PATSecond: quotaBoundary(10, 11), PATMinute: quotaBoundary(120, 121), UserStreams: quotaBoundary(20, 21), ServerStreams: quotaBoundary(40, 41)},
		PathLockStripes: 1024, DuplicateOperations: 0, ResourceDrift: 0, RSSBounded: true,
		Cleanup: StressCleanupSummary{Passed: true, ReceiptCount: 9},
	}
}
