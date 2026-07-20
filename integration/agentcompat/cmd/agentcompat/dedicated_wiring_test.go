package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/scenario"
)

func TestCLI_SelectsTransferAndReconnectWithTypedEvidence(t *testing.T) {
	tests := []struct {
		name  string
		fault string
	}{
		{contract.ScenarioTransfer100MiB, ""},
		{contract.ScenarioTransfer100MiB, contract.FaultTransferHash},
		{contract.ScenarioReconnect, ""},
		{contract.ScenarioReconnect, contract.FaultDashboardExit},
	}
	for _, test := range tests {
		t.Run(test.name+"/"+test.fault, func(t *testing.T) {
			config := testCLIConfig(t, test.name, test.fault)
			runnerErr := errors.New("dedicated runner sentinel")
			wantResult := scenario.Result{Name: test.name, Passed: false, CleanupOK: true, Error: runnerErr.Error(), Assertions: []scenario.Assertion{{Name: "typed dispatch", Passed: false, Details: test.fault}}}
			wantTransfer := scenario.TransferEvidence{WarmupUploadBytes: 65536, WarmupDownloadBytes: 65536, WarmupSHA256: "warmup", WarmupDuration: time.Second, WarmupDeadlineRemaining: 2 * time.Second, WarmupQuiescent: true, UploadBytes: contract.TransferBytes, DownloadBytes: contract.TransferBytes, UploadSHA256: "transfer-hash", DownloadSHA256: "transfer-hash", UploadChunks: 3, DownloadChunks: 4, UploadDuration: 5 * time.Second, DownloadDuration: 6 * time.Second, RetainedHeapBytes: 7, Mode: "0640", CreateDirs: true, UploadReplayRejected: true, DownloadReplayRejected: true, OversizeRejected: true, OutsideRootSentinelsUnchanged: true}
			wantReconnect := completeReconnectDispatchEvidence(t)
			previousTransfer := runTransferScenario
			previousReconnect := runReconnectScenario
			var receivedTransfer *scenario.TransferInput
			var receivedReconnect *scenario.ReconnectInput
			runTransferScenario = func(_ context.Context, input scenario.TransferInput) (scenario.Result, scenario.TransferEvidence, error) {
				receivedTransfer = &input
				return wantResult, wantTransfer, runnerErr
			}
			runReconnectScenario = func(_ context.Context, input scenario.ReconnectInput) (scenario.Result, scenario.ReconnectEvidence, error) {
				receivedReconnect = &input
				return wantResult, wantReconnect, runnerErr
			}
			t.Cleanup(func() {
				runTransferScenario = previousTransfer
				runReconnectScenario = previousReconnect
			})

			execution, err := selectScenarioExecution(config)
			if err != nil {
				t.Fatalf("select execution: %v", err)
			}
			output, runErr := execution.run(context.Background())
			if !errors.Is(runErr, runnerErr) {
				t.Fatalf("runner error=%v, want sentinel", runErr)
			}
			if err := output.Validate(); err != nil {
				t.Fatalf("validate typed output: %v", err)
			}
			if !reflect.DeepEqual(output.Result, wantResult) {
				t.Fatalf("result=%#v, want %#v", output.Result, wantResult)
			}
			if test.name == contract.ScenarioTransfer100MiB {
				wantInput := scenario.TransferInput{Paths: config.Paths, Fault: config.Fault}
				if receivedTransfer == nil || *receivedTransfer != wantInput || receivedReconnect != nil {
					t.Fatalf("transfer inputs: received=%#v reconnect=%#v want=%#v", receivedTransfer, receivedReconnect, wantInput)
				}
				if output.Transfer == nil || !reflect.DeepEqual(*output.Transfer, wantTransfer) || output.Reconnect != nil {
					t.Fatalf("transfer evidence=%#v reconnect=%#v", output.Transfer, output.Reconnect)
				}
			} else {
				wantInput := scenario.ReconnectInput{Paths: config.Paths, DashboardFault: config.Fault.String()}
				if receivedReconnect == nil || *receivedReconnect != wantInput || receivedTransfer != nil {
					t.Fatalf("reconnect inputs: received=%#v transfer=%#v want=%#v", receivedReconnect, receivedTransfer, wantInput)
				}
				if output.Reconnect == nil || !reflect.DeepEqual(*output.Reconnect, wantReconnect) || output.Transfer != nil {
					t.Fatalf("reconnect evidence=%#v transfer=%#v", output.Reconnect, output.Transfer)
				}
			}
		})
	}
}

func TestCLI_RejectsUnsupportedScenarioFaultPairsBeforeRunner(t *testing.T) {
	tests := []struct{ scenario, fault string }{
		{contract.ScenarioTransfer100MiB, contract.FaultDashboardExit},
		{contract.ScenarioReconnect, contract.FaultTransferHash},
		{contract.ScenarioMCPFilesystem, contract.FaultAgentBadSecret},
	}
	for _, test := range tests {
		called := false
		previousTransfer := runTransferScenario
		runTransferScenario = func(context.Context, scenario.TransferInput) (scenario.Result, scenario.TransferEvidence, error) {
			called = true
			return scenario.Result{}, scenario.TransferEvidence{}, errors.New("unexpected")
		}
		_, err := selectScenarioExecution(testCLIConfig(t, test.scenario, test.fault))
		runTransferScenario = previousTransfer
		if err == nil || called {
			t.Fatalf("unsupported pair started runner: scenario=%q fault=%q err=%v", test.scenario, test.fault, err)
		}
	}
}

func TestCLI_WritesDedicatedArtifactWithPrivateMode(t *testing.T) {
	resultsDir := t.TempDir()
	output := scenarioExecutionOutput{
		Result:   scenario.Result{Name: contract.ScenarioTransfer100MiB, Passed: false, CleanupOK: true, Error: "transfer scenario: injected hash mismatch"},
		Transfer: &scenario.TransferEvidence{WarmupUploadBytes: 65536, WarmupDownloadBytes: 65536, WarmupSHA256: "abc", WarmupDuration: time.Nanosecond, WarmupDeadlineRemaining: time.Second, WarmupQuiescent: true, OutsideRootSentinelsUnchanged: true},
	}
	config := testCLIConfig(t, contract.ScenarioTransfer100MiB, contract.FaultTransferHash)
	paths, err := contract.NewPaths(config.Paths.NezhaSource().String(), config.Paths.AgentSource().String(), resultsDir)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	config.Paths = paths
	if err := writeScenarioEvidence(config, output, time.Now()); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	info, err := os.Stat(filepath.Join(resultsDir, "transfer.json"))
	if err != nil {
		t.Fatalf("stat transfer evidence: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("transfer evidence mode=%o", info.Mode().Perm())
	}
}

func testCLIConfig(t *testing.T, scenarioName, faultName string) cliConfig {
	t.Helper()
	paths, err := contract.NewPaths("/src/nezha", "/src/agent", t.TempDir())
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	profile, err := contract.ProfileByName("pr-full")
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	scenarioValue, err := contract.NewScenario(scenarioName)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	fault := contract.Fault{}
	if faultName != "" {
		fault, err = contract.NewFault(faultName)
		if err != nil {
			t.Fatalf("fault: %v", err)
		}
	}
	return cliConfig{Paths: paths, Profile: profile, Seed: contract.DefaultSeed, Scenarios: scenarioFlags{scenarioValue}, Fault: fault}
}
