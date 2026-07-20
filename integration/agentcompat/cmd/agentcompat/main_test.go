//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/scenario"
)

func TestCLI_ParsesTypedFlagsAndWritesMetadata(t *testing.T) {
	resultsDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := run([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", resultsDir, "--seed", "0x4e5a4841", "--scenario", "metadata"}, &stdout, &stderr, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("run CLI: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(resultsDir, "metadata.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var metadata struct {
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
		Seed string `json:"seed"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("parse metadata: %v", err)
	}
	if metadata.Profile.Name != "pr-full" || metadata.Seed != "0x4e5a4841" || !strings.Contains(stdout.String(), "metadata written") {
		t.Fatalf("unexpected metadata-only output: %#v %q", metadata, stdout.String())
	}
}

func TestCLI_MetadataEvidenceValidatesAsCurrentMetadataProfile(t *testing.T) {
	resultsDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := run([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", resultsDir, "--scenario", "metadata"}, &stdout, &stderr, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("run metadata CLI: %v", err)
	}
	if err := evidence.ValidateDirectory(resultsDir); err != nil {
		t.Fatalf("validate metadata evidence: %v", err)
	}
}

func TestCLI_RejectsInvalidProfileAndSeedWithoutSecretEcho(t *testing.T) {
	for name, args := range map[string][]string{
		"profile": {"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "private-profile-secret", "--results-dir", t.TempDir()},
		"seed":    {"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", t.TempDir(), "--seed", "not-a-seed-secret"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(args, &stdout, &stderr, time.Now())
			if err == nil {
				t.Fatal("invalid CLI input accepted")
			}
			if strings.Contains(err.Error(), "not-a-seed-secret") || strings.Contains(err.Error(), "private-profile-secret") {
				t.Fatal("invalid input echoed in error")
			}
		})
	}
}

func TestCLI_RejectsMissingPaths(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--profile", "pr-full", "--scenario", "metadata"}, &stdout, &stderr, time.Now())
	if err == nil || !strings.Contains(err.Error(), "--nezha-source") {
		t.Fatalf("missing source paths were not rejected: %v", err)
	}
}

func TestCLI_ParsesRepeatableScenariosAndFault(t *testing.T) {
	var stderr bytes.Buffer
	config, err := parseFlags([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "soak", "--results-dir", "/tmp/results", "--scenario", "metadata", "--scenario", "transfer-100mib", "--fault", "transfer-hash"}, &stderr)
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if len(config.Scenarios) != 2 || config.Scenarios[0].String() != "metadata" || config.Scenarios[1].String() != "transfer-100mib" || config.Fault.String() != "transfer-hash" {
		t.Fatalf("unexpected typed flags: %#v", config)
	}
}

func TestCLI_WritesMetadataBeforeRejectingUnsupportedRuntime(t *testing.T) {
	resultsDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := run([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", resultsDir, "--scenario", "future-scenario"}, &stdout, &stderr, time.Now())
	if err == nil {
		t.Fatal("unimplemented runtime reported success")
	}
	if _, statErr := os.Stat(filepath.Join(resultsDir, "metadata.json")); statErr != nil {
		t.Fatalf("metadata was not written before runtime rejection: %v", statErr)
	}
}

func TestCLI_RecognizesOnlyMCPFilesystemRuntimeScenario(t *testing.T) {
	var stderr bytes.Buffer
	config, err := parseFlags([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", t.TempDir(), "--scenario", "mcp-filesystem"}, &stderr)
	if err != nil {
		t.Fatalf("parse mcp-filesystem flags: %v", err)
	}
	execution, err := selectScenarioExecution(config)
	if err != nil || execution.name != "mcp-filesystem" {
		t.Fatalf("mcp-filesystem runtime registration: name=%q err=%v", execution.name, err)
	}

	config.Scenarios = append(config.Scenarios, config.Scenarios[0])
	if _, err := selectScenarioExecution(config); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("multi-scenario runtime was unexpectedly accepted: %v", err)
	}
}

func TestCLI_SelectsTerminalRuntimeScenario(t *testing.T) {
	var stderr bytes.Buffer
	config, err := parseFlags([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", t.TempDir(), "--scenario", "terminal"}, &stderr)
	if err != nil {
		t.Fatalf("parse terminal flags: %v", err)
	}
	previous := runTerminalScenario
	var received scenario.TerminalInput
	runTerminalScenario = func(_ context.Context, input scenario.TerminalInput) (scenario.Result, error) {
		received = input
		return scenario.Result{Name: "terminal", Passed: true}, nil
	}
	t.Cleanup(func() { runTerminalScenario = previous })

	execution, err := selectScenarioExecution(config)

	if err != nil {
		t.Fatalf("select terminal scenario: %v", err)
	}
	if execution.name != "terminal" || execution.run == nil {
		t.Fatalf("unexpected terminal execution: %#v", execution)
	}
	if _, err := execution.run(context.Background()); err != nil {
		t.Fatalf("run terminal execution: %v", err)
	}
	if received.Paths.NezhaSource().String() != "/src/nezha" || received.Paths.AgentSource().String() != "/src/agent" || !received.Fault.IsZero() {
		t.Fatalf("terminal input was not forwarded: %#v", received)
	}
}

func TestCLI_MetadataWriteReplacesSymlinkWithoutChangingTarget(t *testing.T) {
	resultsDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("sentinel"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(resultsDir, "metadata.json")); err != nil {
		t.Fatalf("create metadata symlink: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", resultsDir, "--scenario", "metadata"}, &stdout, &stderr, time.Now())
	if err != nil {
		t.Fatalf("replace metadata symlink: %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "sentinel" {
		t.Fatalf("symlink target changed: %q %v", data, readErr)
	}
	info, statErr := os.Lstat(filepath.Join(resultsDir, "metadata.json"))
	if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("metadata replacement mode=%v err=%v", info, statErr)
	}
}

func TestCLI_MetadataWriteUsesPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	resultsDir := filepath.Join(root, "results")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", resultsDir, "--scenario", "metadata"}, &stdout, &stderr, time.Now()); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	directoryInfo, err := os.Stat(resultsDir)
	if err != nil {
		t.Fatalf("stat results directory: %v", err)
	}
	fileInfo, err := os.Stat(filepath.Join(resultsDir, "metadata.json"))
	if err != nil {
		t.Fatalf("stat metadata: %v", err)
	}
	if directoryInfo.Mode().Perm() != 0o700 || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected permissions: directory=%o file=%o", directoryInfo.Mode().Perm(), fileInfo.Mode().Perm())
	}
}
