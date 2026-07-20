package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

func runContext(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	config, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}
	if err := writeMetadata(ctx, config, now); err != nil {
		return err
	}
	if len(config.Scenarios) == 1 && config.Scenarios[0].String() == contract.ScenarioMetadata && config.Fault.IsZero() {
		fmt.Fprintf(stdout, "metadata written for profile %s\n", config.Profile.Name())
		return nil
	}
	execution, err := selectScenarioExecution(config)
	if err != nil {
		return err
	}
	scenarioContext, cancel := context.WithTimeout(ctx, config.Profile.SuiteDeadline())
	defer cancel()
	output, runErr := execution.run(scenarioContext)
	if err := writeScenarioEvidence(config, output, now); err != nil {
		return err
	}
	if err := evidence.ValidateDirectory(config.Paths.ResultsDir().String()); err != nil {
		return fmt.Errorf("validate scenario evidence: %w", err)
	}
	if runErr != nil {
		return runErr
	}
	fmt.Fprintf(stdout, "scenario %s passed for profile %s\n", execution.name, config.Profile.Name())
	return nil
}

func run(args []string, stdout io.Writer, stderr io.Writer, now time.Time) error {
	return runContext(context.Background(), args, stdout, stderr, now)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "agentcompat: %s\n", evidence.Redact(err.Error()))
		os.Exit(2)
	}
}
