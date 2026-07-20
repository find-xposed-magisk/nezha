package workflowpolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const agentcompatStressTestName = "TestStressPRFullEightAgentExactlyOnce"

func TestPolicy_NezhaStressWorkflowRunsPinnedCrossRepositoryTest(t *testing.T) {
	// Given
	data := readNezhaQualityWorkflow(t)
	var workflow qualityWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))

	// When
	stressJob, exists := workflow.Jobs["agentcompat-stress"]

	// Then
	require.True(t, exists)
	require.Equal(t, "Linux agent compatibility stress", stressJob.Name)
	require.Equal(t, "ubuntu-24.04", stressJob.RunsOn)
	require.Equal(t, 75, stressJob.TimeoutMinutes)
	require.Len(t, stressJob.Steps, 5)

	nezhaCheckout := stressJob.stepNamed(t, "Checkout Nezha revision")
	require.Equal(t, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", nezhaCheckout.Uses)
	require.Empty(t, nezhaCheckout.With.Repository)
	require.Empty(t, nezhaCheckout.With.Ref)
	require.Equal(t, "nezha", nezhaCheckout.With.Path)
	require.False(t, *nezhaCheckout.With.PersistCredentials)

	agentCheckout := stressJob.stepNamed(t, "Checkout pinned Agent revision")
	require.Equal(t, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0", agentCheckout.Uses)
	require.Equal(t, "nezhahq/agent", agentCheckout.With.Repository)
	require.Equal(t, "667e1dd5e166ffef808ec26dc20de85bc33a0a0f", agentCheckout.With.Ref)
	require.Equal(t, "agent", agentCheckout.With.Path)
	require.False(t, *agentCheckout.With.PersistCredentials)

	setupGo := stressJob.stepNamed(t, "Set up Go")
	require.Equal(t, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16", setupGo.Uses)
	require.Equal(t, "1.26.x", setupGo.With.GoVersion)
	require.False(t, *setupGo.With.Cache)

	listStep := stressJob.stepNamed(t, "Require named stress test")
	require.Equal(t, "nezha", listStep.WorkingDirectory)
	require.Equal(t, "go test -mod=readonly -tags=agentcompat -list '^"+agentcompatStressTestName+"$' ./integration/agentcompat/internal/scenario | grep -Fx '"+agentcompatStressTestName+"'", listStep.Run)

	runStep := stressJob.stepNamed(t, "Run PR-full agent compatibility stress")
	require.Equal(t, "nezha", runStep.WorkingDirectory)
	require.Equal(t, "${{ github.workspace }}/nezha", runStep.Env.AgentcompatNezhaSource)
	require.Equal(t, "${{ github.workspace }}/agent", runStep.Env.AgentcompatAgentSource)
	require.Equal(t, "go test -mod=readonly -tags=agentcompat -run '^"+agentcompatStressTestName+"$' -count=1 -v ./integration/agentcompat/internal/scenario", runStep.Run)
}
