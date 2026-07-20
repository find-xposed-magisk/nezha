//go:build agentcompat

package workflowpolicy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/workflowpolicy"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type agentQualityWorkflow struct {
	Triggers map[string]agentQualityTrigger `yaml:"on"`
	Jobs     map[string]agentQualityJob     `yaml:"jobs"`
}

type agentQualityTrigger struct {
	Branches    []string `yaml:"branches"`
	Paths       []string `yaml:"paths"`
	PathsIgnore []string `yaml:"paths-ignore"`
}

type agentQualityJob struct {
	Name      string               `yaml:"name"`
	Needs     []string             `yaml:"needs"`
	Condition string               `yaml:"if"`
	Runner    string               `yaml:"runs-on"`
	Strategy  agentQualityStrategy `yaml:"strategy"`
	Steps     []agentQualityStep   `yaml:"steps"`
}

type agentQualityStrategy struct {
	Matrix agentQualityMatrix `yaml:"matrix"`
}

type agentQualityMatrix struct {
	OperatingSystems []string `yaml:"os"`
}

type agentQualityStep struct {
	Run string `yaml:"run"`
}

func TestPolicy_AgentQualityWorkflow(t *testing.T) {
	// Given
	path := filepath.Join("..", "..", "..", "..", "..", "agent", ".github", "workflows", "test.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, workflowpolicy.Verify(data, workflowpolicy.RepositoryAgent))
	var workflow agentQualityWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))

	// When
	ordinaryJob, hasOrdinaryJob := workflow.Jobs["tests"]
	qualityJob, hasQualityJob := workflow.Jobs["linux-race-quality"]
	stressJob, hasStressJob := workflow.Jobs["agentcompat-stress"]
	aggregator, hasAggregator := workflow.Jobs["agent-quality-required"]

	// Then
	require.True(t, hasOrdinaryJob)
	require.True(t, hasQualityJob)
	require.True(t, hasStressJob)
	require.True(t, hasAggregator)
	require.Len(t, workflow.Jobs, 4)
	require.Equal(t, []string{"main"}, workflow.Triggers["push"].Branches)
	require.Equal(t, []string{"main"}, workflow.Triggers["pull_request"].Branches)
	require.Contains(t, workflow.Triggers, "merge_group")
	for _, trigger := range workflow.Triggers {
		require.Empty(t, trigger.Paths)
		require.Empty(t, trigger.PathsIgnore)
	}
	require.ElementsMatch(t, []string{"ubuntu-latest", "windows-latest", "macos-latest"}, ordinaryJob.Strategy.Matrix.OperatingSystems)
	requireWorkflowCommands(t, ordinaryJob.Steps, "go test -mod=readonly -count=1 ./...")
	require.Equal(t, "ubuntu-24.04", qualityJob.Runner)
	requireWorkflowCommands(t, qualityJob.Steps,
		"go test -mod=readonly -race -shuffle=on -count=1 ./...",
		"go vet ./...",
		"test -z \"$(git ls-files -co --exclude-standard '*.go' -z | xargs -0 gofmt -l)\"",
		"go build ./cmd/agent",
	)
	require.NotEmpty(t, stressJob)
	require.ElementsMatch(t, []string{"tests", "linux-race-quality", "agentcompat-stress"}, aggregator.Needs)
	require.Equal(t, "agent-quality-required", aggregator.Name)
	require.Equal(t, "${{ always() }}", aggregator.Condition)
	requireWorkflowCommands(t, aggregator.Steps,
		"test \"${{ needs.tests.result }}\" = success\ntest \"${{ needs.linux-race-quality.result }}\" = success\ntest \"${{ needs.agentcompat-stress.result }}\" = success\n",
	)
}

func requireWorkflowCommands(t *testing.T, steps []agentQualityStep, commands ...string) {
	t.Helper()
	actualCommands := make([]string, 0, len(steps))
	for _, step := range steps {
		if step.Run != "" {
			actualCommands = append(actualCommands, step.Run)
		}
	}
	require.ElementsMatch(t, commands, actualCommands)
}
