//go:build linux

package main

import (
	"bytes"
	"testing"
)

func TestCLI_SelectsNATRuntimeScenario(t *testing.T) {
	// Given
	var stderr bytes.Buffer
	config, err := parseFlags([]string{"--nezha-source", "/src/nezha", "--agent-source", "/src/agent", "--profile", "pr-full", "--results-dir", t.TempDir(), "--scenario", "nat"}, &stderr)
	if err != nil {
		t.Fatalf("parse NAT flags: %v", err)
	}

	// When
	execution, err := selectScenarioExecution(config)

	// Then
	if err != nil {
		t.Fatalf("select NAT scenario: %v", err)
	}
	if execution.name != "nat" || execution.run == nil {
		t.Fatalf("unexpected NAT execution: %#v", execution)
	}
}
