//go:build linux

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

type scenarioFlags []contract.Scenario

func (scenarios *scenarioFlags) String() string {
	values := make([]string, 0, len(*scenarios))
	for _, scenario := range *scenarios {
		values = append(values, scenario.String())
	}
	return strings.Join(values, ",")
}

func (scenarios *scenarioFlags) Set(value string) error {
	scenario, err := contract.NewScenario(value)
	if err != nil {
		return err
	}
	*scenarios = append(*scenarios, scenario)
	return nil
}

type cliConfig struct {
	Paths     contract.Paths
	Profile   contract.Profile
	Seed      contract.Seed
	Scenarios scenarioFlags
	Fault     contract.Fault
}

func parseFlags(args []string, stderr io.Writer) (cliConfig, error) {
	flags := flag.NewFlagSet("agentcompat", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	nezhaSource := flags.String("nezha-source", "", "Nezha source directory")
	agentSource := flags.String("agent-source", "", "Agent source directory")
	profileName := flags.String("profile", "", "compatibility profile")
	resultsDir := flags.String("results-dir", "", "evidence output directory")
	seedValue := flags.String("seed", "0x4e5a4841", "deterministic seed")
	faultName := flags.String("fault", "", "named fault injection")
	var scenarios scenarioFlags
	flags.Var(&scenarios, "scenario", "run only a named scenario; omit for the complete profile")
	if err := flags.Parse(args); err != nil {
		return cliConfig{}, errors.New("invalid command-line arguments")
	}
	paths, err := contract.NewPaths(*nezhaSource, *agentSource, *resultsDir)
	if err != nil {
		return cliConfig{}, err
	}
	if err := prepareResultsDirBeforeParse(paths.ResultsDir().String()); err != nil {
		return cliConfig{}, err
	}
	profile, err := contract.ProfileByName(*profileName)
	if err != nil {
		return cliConfig{}, err
	}
	seed, err := contract.ParseSeed(*seedValue)
	if err != nil {
		return cliConfig{}, err
	}
	fault := contract.Fault{}
	if *faultName != "" {
		fault, err = contract.NewFault(*faultName)
		if err != nil {
			return cliConfig{}, err
		}
	}
	return cliConfig{Paths: paths, Profile: profile, Seed: seed, Scenarios: scenarios, Fault: fault}, nil
}

func prepareResultsDir(resultsDir string) error {
	return prepareEvidenceArtifacts(resultsDir, evidence.FixedEvidenceFiles())
}

func prepareResultsDirBeforeParse(resultsDir string) error {
	return prepareEvidenceArtifacts(resultsDir, evidence.FixedEvidenceFiles()[1:])
}

func prepareEvidenceArtifacts(resultsDir string, artifactNames []string) error {
	if info, err := os.Lstat(resultsDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("results directory must not be a symbolic link")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect results directory: %w", err)
	}
	for _, name := range artifactNames {
		if err := os.Remove(filepath.Join(resultsDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove previous artifact %s: %w", name, err)
		}
	}
	if err := os.RemoveAll(filepath.Join(resultsDir, "agents")); err != nil {
		return fmt.Errorf("remove previous agent logs: %w", err)
	}
	return nil
}
