//go:build agentcompat

package workflowpolicy_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const agentWorkflowStressTestName = "TestStressPRFullEightAgentExactlyOnce"

var fullCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func TestPolicy_AgentStressWorkflowRunsPinnedCrossRepositoryTest(t *testing.T) {
	// Given
	path := filepath.Join("..", "..", "..", "..", "..", "agent", ".github", "workflows", "test.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var workflow qualityWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))

	// When
	stressJob, exists := workflow.Jobs["agentcompat-stress"]

	// Then
	require.True(t, exists)
	require.Equal(t, "Linux agent compatibility stress", stressJob.Name)
	require.Equal(t, "ubuntu-24.04", stressJob.RunsOn)
	require.Equal(t, 75, stressJob.TimeoutMinutes)
	require.Len(t, stressJob.Steps, 7)

	agentCheckout := stressJob.stepNamed(t, "Checkout Agent revision")
	require.Equal(t, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", agentCheckout.Uses)
	require.Empty(t, agentCheckout.With.Repository)
	require.Empty(t, agentCheckout.With.Ref)
	require.Equal(t, "agent", agentCheckout.With.Path)
	require.False(t, *agentCheckout.With.PersistCredentials)

	nezhaCheckout := stressJob.stepNamed(t, "Checkout pinned Nezha revision")
	require.Equal(t, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", nezhaCheckout.Uses)
	require.Equal(t, "nezhahq/nezha", nezhaCheckout.With.Repository)
	require.Regexp(t, fullCommitSHA, nezhaCheckout.With.Ref)
	require.Equal(t, "nezha", nezhaCheckout.With.Path)
	require.False(t, *nezhaCheckout.With.PersistCredentials)

	setupGo := stressJob.stepNamed(t, "Set up Go")
	require.Equal(t, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16", setupGo.Uses)
	require.Equal(t, "^1.26.1", setupGo.With.GoVersion)
	require.False(t, *setupGo.With.Cache)

	prepareDashboardInputs := stressJob.stepNamed(t, "Prepare Dashboard build inputs")
	require.Equal(t, "nezha", prepareDashboardInputs.WorkingDirectory)
	require.Equal(t, strings.Join([]string{
		"go install github.com/swaggo/swag/cmd/swag@v1.16.6",
		"mkdir -p cmd/dashboard/user-dist cmd/dashboard/admin-dist",
		"printf 'placeholder\\n' > cmd/dashboard/user-dist/placeholder.txt",
		"printf 'placeholder\\n' > cmd/dashboard/admin-dist/placeholder.txt",
		"swag init --pd -d cmd/dashboard -g main.go -o cmd/dashboard/docs",
	}, "\n"), strings.TrimSpace(prepareDashboardInputs.Run))

	policyStep := stressJob.stepNamed(t, "Require Agent workflow policy tests")
	require.Equal(t, "nezha", policyStep.WorkingDirectory)
	require.Equal(t, "go test -mod=readonly -tags=agentcompat -list '^TestPolicy_AgentQualityWorkflow$' ./integration/agentcompat/internal/workflowpolicy | grep -Fx 'TestPolicy_AgentQualityWorkflow'\ngo test -mod=readonly -tags=agentcompat -list '^TestPolicy_AgentStressWorkflowRunsPinnedCrossRepositoryTest$' ./integration/agentcompat/internal/workflowpolicy | grep -Fx 'TestPolicy_AgentStressWorkflowRunsPinnedCrossRepositoryTest'\ngo test -mod=readonly -tags=agentcompat -run '^(TestPolicy_AgentQualityWorkflow|TestPolicy_AgentStressWorkflowRunsPinnedCrossRepositoryTest)$' -count=1 ./integration/agentcompat/internal/workflowpolicy\n", policyStep.Run)

	listStep := stressJob.stepNamed(t, "Require named stress test")
	require.Equal(t, "nezha", listStep.WorkingDirectory)
	require.Equal(t, "go test -mod=readonly -tags=agentcompat -list '^"+agentWorkflowStressTestName+"$' ./integration/agentcompat/internal/scenario | grep -Fx '"+agentWorkflowStressTestName+"'", listStep.Run)

	runStep := stressJob.stepNamed(t, "Run PR-full agent compatibility stress")
	require.Equal(t, "nezha", runStep.WorkingDirectory)
	require.Equal(t, "${{ github.workspace }}/nezha", runStep.Env.AgentcompatNezhaSource)
	require.Equal(t, "${{ github.workspace }}/agent", runStep.Env.AgentcompatAgentSource)
	require.Equal(t, "go test -mod=readonly -tags=agentcompat -run '^"+agentWorkflowStressTestName+"$' -count=1 -v ./integration/agentcompat/internal/scenario", runStep.Run)

	require.Equal(t, []string{
		"Checkout Agent revision",
		"Checkout pinned Nezha revision",
		"Set up Go",
		"Prepare Dashboard build inputs",
		"Require Agent workflow policy tests",
		"Require named stress test",
		"Run PR-full agent compatibility stress",
	}, stepNames(stressJob.Steps))
}

func stepNames(steps []qualityStep) []string {
	names := make([]string, len(steps))
	for index, step := range steps {
		names[index] = step.Name
	}
	return names
}
