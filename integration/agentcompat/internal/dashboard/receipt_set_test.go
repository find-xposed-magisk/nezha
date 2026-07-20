//go:build linux && agentcompat

package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/nezhahq/nezha/model"
	"github.com/stretchr/testify/require"
)

func TestMCPReceiptSet_MatchesUnorderedExactServerTaskIdentities(t *testing.T) {
	expectations := []MCPReceiptExpectation{
		{DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec},
		{DashboardGeneration: 2, GateGeneration: 9, ServerID: 8, TaskID: 102, TaskType: model.TaskTypeFsRead},
	}
	events := []MCPReceiptEvent{
		mcpReceiptEvent(1, 2, 9, 8, 102, model.TaskTypeFsRead, MCPReceiptTask),
		mcpReceiptEvent(2, 2, 9, 8, 102, model.TaskTypeFsRead, MCPReceiptResult),
		mcpReceiptEvent(3, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptTask),
		mcpReceiptEvent(4, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptResult),
	}

	pairs, complete, err := matchMCPReceiptSet(events, MCPReceiptCursor{}, expectations)

	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, uint64(7), pairs[0].Task.ServerID)
	require.Equal(t, uint64(101), pairs[0].Result.TaskID)
	require.Equal(t, uint64(8), pairs[1].Task.ServerID)
	require.Equal(t, uint64(102), pairs[1].Result.TaskID)
}

func TestMCPReceiptSet_RejectsMissingDuplicateAndMismatchedReceipts(t *testing.T) {
	expectation := []MCPReceiptExpectation{{DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec}}
	task := mcpReceiptEvent(1, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptTask)

	tests := map[string][]MCPReceiptEvent{
		"missing result":                       {task},
		"duplicate task identity":              {task, mcpReceiptEvent(2, 2, 9, 7, 102, model.TaskTypeExec, MCPReceiptTask)},
		"duplicate task ID":                    {task, mcpReceiptEvent(2, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptTask)},
		"mismatched server":                    {mcpReceiptEvent(1, 2, 9, 8, 101, model.TaskTypeExec, MCPReceiptTask)},
		"result gate generation mismatch":      {task, mcpReceiptEvent(2, 2, 10, 7, 101, model.TaskTypeExec, MCPReceiptResult)},
		"result dashboard generation mismatch": {task, mcpReceiptEvent(2, 3, 9, 7, 101, model.TaskTypeExec, MCPReceiptResult)},
	}
	for name, events := range tests {
		t.Run(name, func(t *testing.T) {
			_, complete, err := matchMCPReceiptSet(events, MCPReceiptCursor{}, expectation)
			if name == "missing result" {
				require.NoError(t, err)
				require.False(t, complete)
				return
			}
			require.Error(t, err)
			require.False(t, complete)
		})
	}
}

func TestMCPReceiptSet_WaitsForEventAndPreservesCursorGeneration(t *testing.T) {
	dashboard := newWaiterDashboard()
	dashboard.eventGeneration = 2
	dashboard.mcpReceiptEvents = []MCPReceiptEvent{
		mcpReceiptEvent(1, 1, 8, 7, 100, model.TaskTypeExec, MCPReceiptTask),
		mcpReceiptEvent(2, 1, 8, 7, 100, model.TaskTypeExec, MCPReceiptResult),
	}
	dashboard.mcpReceiptSequence = 2
	cursor := dashboard.MCPReceiptCursor()
	result := make(chan struct {
		pairs []MCPReceiptPair
		err   error
	}, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() {
		pairs, err := dashboard.WaitForMCPReceiptSet(ctx, cursor, []MCPReceiptExpectation{{DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec}})
		result <- struct {
			pairs []MCPReceiptPair
			err   error
		}{pairs: pairs, err: err}
	}()

	dashboard.eventMu.Lock()
	dashboard.mcpReceiptEvents = append(dashboard.mcpReceiptEvents,
		mcpReceiptEvent(3, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptTask),
		mcpReceiptEvent(4, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptResult),
	)
	dashboard.mcpReceiptSequence = 4
	close(dashboard.eventNotify)
	dashboard.eventNotify = make(chan struct{})
	dashboard.eventMu.Unlock()

	received := <-result
	require.NoError(t, received.err)
	require.Len(t, received.pairs, 1)
	require.Equal(t, uint64(101), received.pairs[0].Task.TaskID)
	require.Equal(t, uint64(2), received.pairs[0].Task.DashboardGeneration)
	require.Equal(t, uint64(9), received.pairs[0].Task.GateGeneration)
}

func TestMCPReceiptSet_RespectsCancellationAndDeadline(t *testing.T) {
	dashboard := newWaiterDashboard()
	expectations := []MCPReceiptExpectation{{DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec}}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := dashboard.WaitForMCPReceiptSet(cancelled, MCPReceiptCursor{}, expectations)
	require.ErrorIs(t, err, context.Canceled)

	expired, expire := context.WithDeadline(t.Context(), time.Now())
	defer expire()
	_, err = dashboard.WaitForMCPReceiptSet(expired, MCPReceiptCursor{}, expectations)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestMCPReceiptSet_RejectsDuplicateExpectations(t *testing.T) {
	_, err := (&Dashboard{}).WaitForMCPReceiptSet(t.Context(), MCPReceiptCursor{}, []MCPReceiptExpectation{
		{DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec},
		{DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec},
	})
	require.Error(t, err)
}

func TestMCPReceiptSet_RejectsWrongExpectedGenerationAndTaskID(t *testing.T) {
	// Given
	events := []MCPReceiptEvent{
		mcpReceiptEvent(1, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptTask),
		mcpReceiptEvent(2, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptResult),
	}
	expectation := MCPReceiptExpectation{DashboardGeneration: 3, GateGeneration: 9, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec}

	// When
	_, _, err := matchMCPReceiptSet(events, MCPReceiptCursor{}, []MCPReceiptExpectation{expectation})

	// Then
	require.Error(t, err)
}

func TestMCPReceiptSet_RejectsZeroGateGenerationExpectation(t *testing.T) {
	// Given
	dashboard := newWaiterDashboard()
	dashboard.eventGeneration = 2
	dashboard.mcpReceiptEvents = []MCPReceiptEvent{
		mcpReceiptEvent(1, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptTask),
		mcpReceiptEvent(2, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptResult),
	}
	expectation := []MCPReceiptExpectation{{DashboardGeneration: 2, GateGeneration: 0, ServerID: 7, TaskID: 101, TaskType: model.TaskTypeExec}}

	// When
	_, err := dashboard.WaitForMCPReceiptSet(t.Context(), MCPReceiptCursor{}, expectation)

	// Then
	require.Error(t, err)
}

func TestMCPReceiptSet_RejectsZeroTaskIDExpectation(t *testing.T) {
	// Given
	dashboard := newWaiterDashboard()
	dashboard.eventGeneration = 2
	dashboard.mcpReceiptEvents = []MCPReceiptEvent{
		mcpReceiptEvent(1, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptTask),
		mcpReceiptEvent(2, 2, 9, 7, 101, model.TaskTypeExec, MCPReceiptResult),
	}
	expectation := []MCPReceiptExpectation{{DashboardGeneration: 2, GateGeneration: 9, ServerID: 7, TaskID: 0, TaskType: model.TaskTypeExec}}

	// When
	_, err := dashboard.WaitForMCPReceiptSet(t.Context(), MCPReceiptCursor{}, expectation)

	// Then
	require.Error(t, err)
}

func mcpReceiptEvent(sequence, dashboardGeneration, gateGeneration, serverID, taskID, taskType uint64, kind MCPReceiptKind) MCPReceiptEvent {
	return MCPReceiptEvent{Sequence: sequence, DashboardGeneration: dashboardGeneration, GateGeneration: gateGeneration, ServerID: serverID, TaskID: taskID, TaskType: taskType, Kind: kind}
}
