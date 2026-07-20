//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestCLI_MetadataCancellationRemovesTemporaryArtifact(t *testing.T) {
	resultsDir := t.TempDir()
	config := testCLIConfig(t, contract.ScenarioMetadata, "")
	paths, err := contract.NewPaths(config.Paths.NezhaSource().String(), config.Paths.AgentSource().String(), resultsDir)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	config.Paths = paths
	ready := make(chan struct{})
	previous := metadataArtifactReady
	metadataArtifactReady = func(ctx context.Context) error {
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { metadataArtifactReady = previous })
	ctx, cancel := context.WithCancel(context.Background())
	writeDone := make(chan error, 1)
	go func() { writeDone <- writeMetadata(ctx, config, time.Now()) }()
	<-ready
	cancel()

	if err := <-writeDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("metadata cancellation error=%v, want context canceled", err)
	}
	if _, err := os.Stat(filepath.Join(resultsDir, "metadata.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata final file exists after cancellation: %v", err)
	}
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		t.Fatalf("read results directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".artifact-") {
			t.Fatalf("metadata temporary artifact survived cancellation: %s", entry.Name())
		}
	}
}
