package contract

import (
	"testing"
	"time"
)

func TestContract_Profiles(t *testing.T) {
	pr, err := ProfileByName("pr-full")
	if err != nil {
		t.Fatalf("parse pr-full: %v", err)
	}
	if pr.JobTimeout() != 75*time.Minute || pr.SuiteDeadline() != 55*time.Minute {
		t.Fatalf("unexpected pr-full deadlines: %#v", pr)
	}
	if pr.Seed() != Seed(0x4e5a4841) || pr.AgentCount() != 8 || pr.StressRounds() != 4 || pr.ConcurrentOperations() != 64 {
		t.Fatalf("unexpected pr-full load: %#v", pr)
	}
	if pr.ConcurrentSessions() != 4 || pr.TransferPairs() != 1 || pr.DashboardRestartCycles() != 1 {
		t.Fatalf("unexpected pr-full sessions: %#v", pr)
	}

	soak, err := ProfileByName("soak")
	if err != nil {
		t.Fatalf("parse soak: %v", err)
	}
	if soak.SuiteDeadline() != 150*time.Minute || soak.AgentCount() != 20 || soak.Iterations() != 3 || soak.DashboardRestartCycles() != 10 || soak.TransferPairs() != 5 || !soak.StreamBoundaryCheck() {
		t.Fatalf("unexpected soak profile: %#v", soak)
	}
	if soak.JobTimeout() != 150*time.Minute || soak.Seed() != DefaultSeed || soak.StreamBoundaryAllowed() != 40 || soak.StreamBoundaryRejected() != 41 || soak.TransferBytes() != 100*1024*1024 {
		t.Fatalf("unexpected soak limits: %#v", soak)
	}
	if _, err := ProfileByName("unknown"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestContract_ResourceBudget(t *testing.T) {
	budget, err := NewResourceBudget(ResourceBudgetInput{
		WarmupRuns:             1,
		SampleCount:            5,
		SampleInterval:         250 * time.Millisecond,
		ChildProcessCountDrift: 0,
		ListenerCountDrift:     0,
		NonStdioFDCountDrift:   0,
		DashboardRSSDeltaBytes: 64 * 1024 * 1024,
		AgentRSSDeltaBytes:     32 * 1024 * 1024,
		TransferHeapBytes:      16 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("construct budget: %v", err)
	}
	if budget.WarmupRuns() != 1 || budget.SampleCount() != 5 || budget.SampleInterval() != 250*time.Millisecond || budget.ChildProcessCountDrift() != 0 || budget.ListenerCountDrift() != 0 || budget.NonStdioFDCountDrift() != 0 {
		t.Fatalf("unexpected sampling budget: %#v", budget)
	}
	if budget.DashboardRSSDeltaBytes() != 64*1024*1024 || budget.AgentRSSDeltaBytes() != 32*1024*1024 || budget.TransferHeapBytes() != 16*1024*1024 {
		t.Fatalf("unexpected memory budget: %#v", budget)
	}
}

func TestContract_ResourceBudgetRejectsInvalidInput(t *testing.T) {
	base := ResourceBudgetInput{WarmupRuns: 1, SampleCount: 5, SampleInterval: 250 * time.Millisecond, DashboardRSSDeltaBytes: 1, AgentRSSDeltaBytes: 1, TransferHeapBytes: 1}
	for name, mutate := range map[string]func(*ResourceBudgetInput){
		"zero warmup":                 func(input *ResourceBudgetInput) { input.WarmupRuns = 0 },
		"zero samples":                func(input *ResourceBudgetInput) { input.SampleCount = 0 },
		"negative samples":            func(input *ResourceBudgetInput) { input.SampleCount = -1 },
		"zero interval":               func(input *ResourceBudgetInput) { input.SampleInterval = 0 },
		"negative interval":           func(input *ResourceBudgetInput) { input.SampleInterval = -time.Millisecond },
		"negative child drift":        func(input *ResourceBudgetInput) { input.ChildProcessCountDrift = -1 },
		"missing dashboard threshold": func(input *ResourceBudgetInput) { input.DashboardRSSDeltaBytes = 0 },
		"missing agent threshold":     func(input *ResourceBudgetInput) { input.AgentRSSDeltaBytes = 0 },
		"missing heap threshold":      func(input *ResourceBudgetInput) { input.TransferHeapBytes = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if _, err := NewResourceBudget(input); err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}

func TestContract_CLIValues(t *testing.T) {
	paths, err := NewPaths("/src/nezha", "/src/agent", "/tmp/results")
	if err != nil {
		t.Fatalf("construct paths: %v", err)
	}
	if paths.NezhaSource().String() != "/src/nezha" || paths.AgentSource().String() != "/src/agent" || paths.ResultsDir().String() != "/tmp/results" {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	scenario, err := NewScenario("metadata")
	if err != nil || scenario.String() != "metadata" {
		t.Fatalf("construct scenario: %q %v", scenario.String(), err)
	}
	fault, err := NewFault("transfer-hash")
	if err != nil || fault.String() != "transfer-hash" {
		t.Fatalf("construct fault: %q %v", fault.String(), err)
	}
	for _, invalid := range []string{"", "../escape", "has space"} {
		if _, err := NewScenario(invalid); err == nil {
			t.Fatalf("invalid scenario accepted: %q", invalid)
		}
	}
}
