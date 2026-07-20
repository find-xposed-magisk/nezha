//go:build linux && agentcompat

package dashboard

import (
	"fmt"
	"testing"

	"github.com/nezhahq/nezha/model"
	"github.com/stretchr/testify/require"
)

func TestMCPReceiptLifecycle_ParsesGenerationScopedTaskAndResultAfterCursor(t *testing.T) {
	// Given
	dashboard := &Dashboard{eventNotify: make(chan struct{}), eventGeneration: 2}
	cursor := dashboard.MCPReceiptCursor()

	// When
	dashboard.processReceiptLineForGeneration(2, fmt.Sprintf("task 9 7 101 %d\n", model.TaskTypeExec))
	dashboard.processReceiptLineForGeneration(2, fmt.Sprintf("result 9 7 101 %d\n", model.TaskTypeExec))
	pairs, err := dashboard.WaitForMCPReceiptPairs(t.Context(), cursor, []MCPReceiptExpectation{{ServerID: 7, TaskType: model.TaskTypeExec}})

	// Then
	require.NoError(t, err)
	require.Len(t, pairs, 1)
	require.Equal(t, uint64(101), pairs[0].Task.TaskID)
	require.Equal(t, pairs[0].Task.TaskID, pairs[0].Result.TaskID)
	require.Equal(t, uint64(2), pairs[0].Task.DashboardGeneration)
	require.Equal(t, uint64(9), pairs[0].Task.GateGeneration)
}

func TestMCPReceiptLifecycle_RejectsDuplicateTaskIDAfterCursor(t *testing.T) {
	// Given
	events := []MCPReceiptEvent{
		{Sequence: 1, DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec, Kind: MCPReceiptTask},
		{Sequence: 2, DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 102, TaskType: model.TaskTypeFsRead, Kind: MCPReceiptTask},
		{Sequence: 3, DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec, Kind: MCPReceiptResult},
		{Sequence: 4, DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec, Kind: MCPReceiptResult},
	}

	// When
	_, _, err := matchMCPReceiptPairs(events, MCPReceiptCursor{}, []MCPReceiptExpectation{{ServerID: 7, TaskType: model.TaskTypeExec}, {ServerID: 7, TaskType: model.TaskTypeFsRead}})

	// Then
	require.Error(t, err)
}

func TestMCPReceiptLifecycle_DiscardsStaleDashboardGeneration(t *testing.T) {
	// Given
	dashboard := &Dashboard{eventNotify: make(chan struct{}), eventGeneration: 2}

	// When
	dashboard.processReceiptLineForGeneration(1, fmt.Sprintf("task 8 7 101 %d\n", model.TaskTypeExec))

	// Then
	require.Empty(t, dashboard.MCPReceiptEventsAfter(MCPReceiptCursor{}))
}

func TestMCPReceiptLifecycle_DoesNotAppendOldGenerationAfterReplacement(t *testing.T) {
	dashboard := &Dashboard{eventNotify: make(chan struct{}), eventGeneration: 2}
	dashboard.processReceiptLineForGeneration(1, fmt.Sprintf("task 8 7 101 %d\n", model.TaskTypeExec))
	require.Empty(t, dashboard.MCPReceiptEventsAfter(MCPReceiptCursor{}))
}
