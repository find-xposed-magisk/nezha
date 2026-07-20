//go:build linux

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/scenario"
)

func TestCLI_RegisteredScenariosHaveExhaustiveRuntimeRouting(t *testing.T) {
	for _, definition := range contract.ScenarioDefinitions() {
		t.Run(definition.Name, func(t *testing.T) {
			config := testCLIConfig(t, definition.Name, "")
			execution, err := selectScenarioExecution(config)
			if definition.Execution == contract.ScenarioExecutionMetadata {
				if err == nil {
					t.Fatal("metadata unexpectedly received runtime execution")
				}
				return
			}
			if err != nil {
				t.Fatalf("select registered scenario: %v", err)
			}
			if execution.name != definition.Name || execution.run == nil {
				t.Fatalf("incomplete runtime execution: %#v", execution)
			}
		})
	}
}

func TestCLI_Todos11To15DispatchPropagatesTypedInputsErrorsAndOutputs(t *testing.T) {
	runnerError := errors.New("injected runner error")
	tests := []struct {
		name  string
		fault string
		set   func(*testing.T, scenario.Result, error, *bool)
	}{
		{contract.ScenarioRegistrationConfigExec, contract.FaultAgentBadSecret, func(t *testing.T, want scenario.Result, wantErr error, called *bool) {
			previous := runRegistrationConfigExecScenario
			runRegistrationConfigExecScenario = func(_ context.Context, input scenario.RegistrationConfigExecInput) (scenario.Result, error) {
				*called = true
				assertPathsAndFault(t, input.Paths, input.Fault, contract.FaultAgentBadSecret)
				return want, wantErr
			}
			t.Cleanup(func() { runRegistrationConfigExecScenario = previous })
		}},
		{contract.ScenarioNAT, "", func(t *testing.T, want scenario.Result, wantErr error, called *bool) {
			previous := runNATScenario
			runNATScenario = func(_ context.Context, input scenario.NATInput) (scenario.Result, error) {
				*called = true
				assertPathsAndFault(t, input.Paths, input.Fault, "")
				return want, wantErr
			}
			t.Cleanup(func() { runNATScenario = previous })
		}},
		{contract.ScenarioLegacyFM, contract.FaultAgentBadSecret, func(t *testing.T, want scenario.Result, wantErr error, called *bool) {
			previous := runLegacyFMScenario
			runLegacyFMScenario = func(_ context.Context, input scenario.LegacyFMInput) (scenario.Result, error) {
				*called = true
				assertPathsAndFault(t, input.Paths, input.Fault, contract.FaultAgentBadSecret)
				return want, wantErr
			}
			t.Cleanup(func() { runLegacyFMScenario = previous })
		}},
		{contract.ScenarioTerminal, "", func(t *testing.T, want scenario.Result, wantErr error, called *bool) {
			previous := runTerminalScenario
			runTerminalScenario = func(_ context.Context, input scenario.TerminalInput) (scenario.Result, error) {
				*called = true
				assertPathsAndFault(t, input.Paths, input.Fault, "")
				return want, wantErr
			}
			t.Cleanup(func() { runTerminalScenario = previous })
		}},
		{contract.ScenarioMCPFilesystem, "", func(t *testing.T, want scenario.Result, wantErr error, called *bool) {
			previous := runMCPFilesystemScenario
			runMCPFilesystemScenario = func(_ context.Context, input scenario.MCPFilesystemInput) (scenario.Result, error) {
				*called = true
				if input.Paths.NezhaSource().String() != "/src/nezha" || input.Paths.AgentSource().String() != "/src/agent" {
					t.Fatalf("MCP filesystem paths=%#v", input.Paths)
				}
				return want, wantErr
			}
			t.Cleanup(func() { runMCPFilesystemScenario = previous })
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := scenario.Result{Name: test.name, Passed: false, Assertions: []scenario.Assertion{{Name: "runner assertion", Passed: false}}, Error: runnerError.Error()}
			called := false
			test.set(t, want, runnerError, &called)
			execution, err := selectScenarioExecution(testCLIConfig(t, test.name, test.fault))
			if err != nil {
				t.Fatalf("select execution: %v", err)
			}
			output, err := execution.run(t.Context())
			if !errors.Is(err, runnerError) {
				t.Fatalf("runner error=%v", err)
			}
			if !called || output.Result.Name != want.Name || output.Result.Error != want.Error || output.Transfer != nil || output.Reconnect != nil {
				t.Fatalf("runner propagation called=%t output=%#v", called, output)
			}
		})
	}
}

func assertPathsAndFault(t *testing.T, paths contract.Paths, fault contract.Fault, wantFault string) {
	t.Helper()
	if paths.NezhaSource().String() != "/src/nezha" || paths.AgentSource().String() != "/src/agent" || fault.String() != wantFault {
		t.Fatalf("paths/fault propagation paths=%#v fault=%q", paths, fault.String())
	}
}
