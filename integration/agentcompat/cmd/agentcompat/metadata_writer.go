package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

var metadataArtifactReady = func(context.Context) error { return nil }

func writeMetadata(ctx context.Context, config cliConfig, now time.Time) error {
	resultsDir := config.Paths.ResultsDir().String()
	if info, err := os.Lstat(resultsDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("results directory must not be a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect results directory: %w", err)
	}
	if err := os.MkdirAll(resultsDir, 0o700); err != nil {
		return fmt.Errorf("create results directory: %w", err)
	}
	if err := os.Chmod(resultsDir, 0o700); err != nil {
		return fmt.Errorf("secure results directory: %w", err)
	}
	if err := prepareResultsDir(resultsDir); err != nil {
		return err
	}
	metadata, err := evidence.NewMetadata(evidence.MetadataInput{Profile: config.Profile, Seed: config.Seed, Paths: config.Paths, ResourceBudget: contract.DefaultResourceBudget(), Scenarios: config.Scenarios, Fault: config.Fault, StartedAt: now, EvidenceFiles: evidence.EvidenceFiles()})
	if err != nil {
		return fmt.Errorf("build metadata: %w", err)
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	path := filepath.Join(resultsDir, "metadata.json")
	return writePrivateArtifactWithSeam(path, data, func() error { return metadataArtifactReady(ctx) })
}
