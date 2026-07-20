package main

import (
	"context"
	"errors"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/scenario"
)

type scenarioExecutionOutput struct {
	Result    scenario.Result
	Transfer  *scenario.TransferEvidence
	Reconnect *scenario.ReconnectEvidence
}

func (output scenarioExecutionOutput) Validate() error {
	if output.Result.Name == "" {
		return errors.New("scenario execution result is missing")
	}
	dedicatedCount := 0
	if output.Transfer != nil {
		dedicatedCount++
	}
	if output.Reconnect != nil {
		dedicatedCount++
	}
	switch output.Result.Name {
	case contract.ScenarioTransfer100MiB:
		if dedicatedCount != 1 || output.Transfer == nil {
			return errors.New("transfer execution requires only transfer evidence")
		}
	case contract.ScenarioReconnect:
		if dedicatedCount != 1 || output.Reconnect == nil {
			return errors.New("reconnect execution requires only reconnect evidence")
		}
	default:
		if dedicatedCount != 0 {
			return errors.New("scenario execution has mismatched dedicated evidence")
		}
	}
	return nil
}

type scenarioExecution struct {
	name string
	run  func(context.Context) (scenarioExecutionOutput, error)
}

var (
	runRegistrationConfigExecScenario = (scenario.RegistrationConfigExec{}).Run
	runNATScenario                    = (scenario.NAT{}).Run
	runLegacyFMScenario               = (scenario.LegacyFM{}).Run
	runTerminalScenario               = (scenario.Terminal{}).Run
	runMCPFilesystemScenario          = (scenario.MCPFilesystem{}).Run
	runTransferScenario               = (scenario.Transfer{}).RunWithEvidence
	runReconnectScenario              = (scenario.Reconnect{}).RunWithEvidence
)

func selectScenarioExecution(config cliConfig) (scenarioExecution, error) {
	if len(config.Scenarios) != 1 {
		return scenarioExecution{}, errors.New("runtime execution requires exactly one --scenario")
	}
	selected := config.Scenarios[0]
	if err := contract.ValidateScenarioFault(selected, config.Fault); err != nil {
		return scenarioExecution{}, err
	}
	definition, err := contract.ScenarioDefinitionByName(selected.String())
	if err != nil {
		return scenarioExecution{}, err
	}
	switch definition.Execution {
	case contract.ScenarioExecutionMetadata:
		return scenarioExecution{}, errors.New("metadata scenario does not have a runtime execution")
	case contract.ScenarioExecutionRegistrationConfigExec:
		return standardExecution(selected.String(), func(ctx context.Context) (scenario.Result, error) {
			return runRegistrationConfigExecScenario(ctx, scenario.RegistrationConfigExecInput{Paths: config.Paths, Fault: config.Fault})
		}), nil
	case contract.ScenarioExecutionNAT:
		return standardExecution(selected.String(), func(ctx context.Context) (scenario.Result, error) {
			return runNATScenario(ctx, scenario.NATInput{Paths: config.Paths, Fault: config.Fault})
		}), nil
	case contract.ScenarioExecutionLegacyFM:
		return standardExecution(selected.String(), func(ctx context.Context) (scenario.Result, error) {
			return runLegacyFMScenario(ctx, scenario.LegacyFMInput{Paths: config.Paths, Fault: config.Fault})
		}), nil
	case contract.ScenarioExecutionTerminal:
		return standardExecution(selected.String(), func(ctx context.Context) (scenario.Result, error) {
			return runTerminalScenario(ctx, scenario.TerminalInput{Paths: config.Paths, Fault: config.Fault})
		}), nil
	case contract.ScenarioExecutionMCPFilesystem:
		return standardExecution(selected.String(), func(ctx context.Context) (scenario.Result, error) {
			return runMCPFilesystemScenario(ctx, scenario.MCPFilesystemInput{Paths: config.Paths})
		}), nil
	case contract.ScenarioExecutionTransfer:
		return scenarioExecution{name: selected.String(), run: func(ctx context.Context) (scenarioExecutionOutput, error) {
			result, transferEvidence, err := runTransferScenario(ctx, scenario.TransferInput{Paths: config.Paths, Fault: config.Fault})
			return scenarioExecutionOutput{Result: result, Transfer: &transferEvidence}, err
		}}, nil
	case contract.ScenarioExecutionReconnect:
		return scenarioExecution{name: selected.String(), run: func(ctx context.Context) (scenarioExecutionOutput, error) {
			result, reconnectEvidence, err := runReconnectScenario(ctx, scenario.ReconnectInput{Paths: config.Paths, DashboardFault: config.Fault.String()})
			return scenarioExecutionOutput{Result: result, Reconnect: &reconnectEvidence}, err
		}}, nil
	default:
		return scenarioExecution{}, errors.New("runtime execution is not implemented for the selected scenario")
	}
}

func standardExecution(name string, run func(context.Context) (scenario.Result, error)) scenarioExecution {
	return scenarioExecution{name: name, run: func(ctx context.Context) (scenarioExecutionOutput, error) {
		result, err := run(ctx)
		return scenarioExecutionOutput{Result: result}, err
	}}
}
