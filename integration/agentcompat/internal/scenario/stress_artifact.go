//go:build linux && agentcompat

package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

var ErrStressArtifactInvalid = errors.New("stress artifact is invalid")

type stressArtifact struct {
	Version                      int                  `json:"version"`
	Profile                      string               `json:"profile"`
	Seed                         string               `json:"seed"`
	RoundCount                   int                  `json:"round_count"`
	OperationCount               int                  `json:"operation_count"`
	SessionCount                 int                  `json:"session_count"`
	WarmupCount                  int                  `json:"warmup_count"`
	ResourceSummaryCount         int                  `json:"resource_summary_count"`
	ResourceSampleCount          int                  `json:"resource_sample_count"`
	ResourceIntervalMilliseconds int64                `json:"resource_interval_milliseconds"`
	Quotas                       StressQuotaEvidence  `json:"quotas"`
	PathLockStripes              int                  `json:"path_lock_stripes"`
	DuplicateOperations          int                  `json:"duplicate_operations"`
	ResourceDrift                int                  `json:"resource_drift"`
	RSSBounded                   bool                 `json:"rss_bounded"`
	Cleanup                      StressCleanupSummary `json:"cleanup"`
}

func stressPRFullProfile() (contract.Profile, error) {
	return contract.ProfileByName(string(contract.ProfilePRFull))
}

func publishStressEvidence(root string, value StressEvidence) error {
	profile, err := stressPRFullProfile()
	if err != nil {
		return err
	}
	if err := value.ValidateSuccess(profile); err != nil {
		return err
	}
	artifact, err := newStressArtifact(value)
	if err != nil {
		return err
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	if evidence.Redact(string(data)) != string(data) {
		return ErrStressArtifactInvalid
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(root, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(root)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return ErrStressArtifactInvalid
	}
	path := filepath.Join(root, "stress.json")
	if stale, statErr := os.Lstat(path); statErr == nil {
		if stale.Mode()&os.ModeSymlink != 0 || !stale.Mode().IsRegular() {
			return ErrStressArtifactInvalid
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	temporary, err := os.CreateTemp(root, ".stress-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func readStressEvidence(root string) (stressArtifact, error) {
	var value stressArtifact
	path := filepath.Join(root, "stress.json")
	info, err := os.Lstat(path)
	if err != nil {
		return value, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return value, ErrStressArtifactInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	if evidence.Redact(string(data)) != string(data) {
		return value, ErrStressArtifactInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode stress artifact: %w", ErrStressArtifactInvalid)
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return value, ErrStressArtifactInvalid
	}
	if err := value.validate(); err != nil {
		return value, err
	}
	return value, nil
}

func newStressArtifact(value StressEvidence) (stressArtifact, error) {
	profile, err := contract.ProfileByName(string(value.Profile))
	if err != nil {
		return stressArtifact{}, err
	}
	artifact := stressArtifact{Version: 1, Profile: string(value.Profile), Seed: fmt.Sprintf("%08x", uint64(value.Seed)), SessionCount: len(value.Sessions), WarmupCount: len(value.Warmups), ResourceSummaryCount: 1 + profile.AgentCount(), ResourceSampleCount: 5, ResourceIntervalMilliseconds: 250, Quotas: value.Quotas, PathLockStripes: value.Quotas.PathLockStripes, Cleanup: value.Cleanup}
	operationIDs := make(map[StressOperationID]struct{})
	resourceEvaluations := make([]StressResourceEvaluation, 0, artifact.ResourceSummaryCount)
	for _, iteration := range value.Iterations {
		artifact.RoundCount += len(iteration.Rounds)
		for _, round := range iteration.Rounds {
			artifact.OperationCount += len(round.Operations)
			for _, operation := range round.Operations {
				if _, duplicate := operationIDs[operation.ID]; duplicate {
					artifact.DuplicateOperations++
				}
				operationIDs[operation.ID] = struct{}{}
			}
		}
		for _, resource := range iteration.Resources {
			evaluation, err := EvaluateStressResource(resource)
			if err != nil {
				return stressArtifact{}, err
			}
			resourceEvaluations = append(resourceEvaluations, evaluation)
		}
	}
	resourceDrift, rssBounded, err := aggregateStressResourceEvaluations(resourceEvaluations, artifact.ResourceSummaryCount)
	if err != nil {
		return stressArtifact{}, err
	}
	artifact.ResourceDrift = resourceDrift
	artifact.RSSBounded = rssBounded
	return artifact, artifact.validate()
}

func aggregateStressResourceEvaluations(evaluations []StressResourceEvaluation, expectedCount int) (int, bool, error) {
	if len(evaluations) != expectedCount {
		return 0, false, fmt.Errorf("resource evaluations=%d want=%d: %w", len(evaluations), expectedCount, ErrStressArtifactInvalid)
	}
	drift := 0
	rssBounded := true
	for _, evaluation := range evaluations {
		if evaluation.Baseline.Descendants != evaluation.End.Descendants || evaluation.Baseline.NonStdioFDs != evaluation.End.NonStdioFDs || evaluation.Baseline.TCPListeners != evaluation.End.TCPListeners || evaluation.Baseline.TCP6Listeners != evaluation.End.TCP6Listeners {
			drift++
		}
		rssBounded = rssBounded && evaluation.RSSDeltaBytes <= evaluation.RSSLimitBytes
	}
	return drift, rssBounded, nil
}

func (artifact stressArtifact) validate() error {
	if artifact.Version != 1 || artifact.Profile != string(contract.ProfilePRFull) || artifact.Seed != "4e5a4841" || artifact.RoundCount != 4 || artifact.OperationCount != 64 || artifact.SessionCount != 12 || artifact.WarmupCount != 8 || artifact.ResourceSummaryCount != 9 || artifact.ResourceSampleCount != 5 || artifact.ResourceIntervalMilliseconds != 250 || artifact.PathLockStripes != 1024 || artifact.DuplicateOperations != 0 || artifact.ResourceDrift != 0 || !artifact.RSSBounded || !artifact.Cleanup.Passed || artifact.Cleanup.ReceiptCount != 9 || artifact.Cleanup.FailedReceiptCount != 0 || artifact.Cleanup.ForcedCleanupCount != 0 || artifact.Cleanup.ProcessResidue != 0 || artifact.Cleanup.ProcessGroupResidue != 0 || artifact.Cleanup.WorkspaceResidue != 0 {
		return ErrStressArtifactInvalid
	}
	return artifact.Quotas.Validate()
}
