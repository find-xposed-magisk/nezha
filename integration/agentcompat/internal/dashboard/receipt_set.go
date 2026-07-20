//go:build linux

package dashboard

import (
	"context"
	"errors"
	"fmt"
)

func (dashboard *Dashboard) WaitForMCPReceiptSet(ctx context.Context, cursor MCPReceiptCursor, expectations []MCPReceiptExpectation) ([]MCPReceiptPair, error) {
	if err := validateMCPReceiptExpectations(expectations); err != nil {
		return nil, err
	}
	for {
		dashboard.eventMu.RLock()
		notify, closed := dashboard.eventNotify, dashboard.eventClosed
		events := append([]MCPReceiptEvent(nil), dashboard.mcpReceiptEvents...)
		dashboard.eventMu.RUnlock()
		pairs, complete, err := matchMCPReceiptSet(events, cursor, expectations)
		if err != nil {
			return nil, err
		}
		if complete {
			return pairs, nil
		}
		if closed {
			return nil, ErrReceiptGateClosed
		}
		select {
		case <-notify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type mcpReceiptIdentity struct {
	dashboardGeneration uint64
	gateGeneration      uint64
	serverID            uint64
	taskID              uint64
	taskType            uint64
}

func validateMCPReceiptExpectations(expectations []MCPReceiptExpectation) error {
	if len(expectations) == 0 {
		return errors.New("MCP receipt expectations are empty")
	}
	seen := make(map[mcpReceiptIdentity]struct{}, len(expectations))
	for _, expectation := range expectations {
		identity := mcpReceiptIdentity{dashboardGeneration: expectation.DashboardGeneration, gateGeneration: expectation.GateGeneration, serverID: expectation.ServerID, taskID: expectation.TaskID, taskType: expectation.TaskType}
		// Stress evidence requires exact generation-aware identity; zero is never a wildcard.
		if expectation.DashboardGeneration == 0 || expectation.GateGeneration == 0 || expectation.ServerID == 0 || expectation.TaskID == 0 || expectation.TaskType == 0 {
			return fmt.Errorf("invalid MCP receipt expectation: %+v", expectation)
		}
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("duplicate MCP receipt expectation for server %d task type %d", expectation.ServerID, expectation.TaskType)
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func matchMCPReceiptSet(events []MCPReceiptEvent, cursor MCPReceiptCursor, expectations []MCPReceiptExpectation) ([]MCPReceiptPair, bool, error) {
	if err := validateMCPReceiptExpectations(expectations); err != nil {
		return nil, false, err
	}
	indices := make(map[mcpReceiptIdentity]int, len(expectations))
	for index, expectation := range expectations {
		indices[mcpReceiptIdentity{dashboardGeneration: expectation.DashboardGeneration, gateGeneration: expectation.GateGeneration, serverID: expectation.ServerID, taskID: expectation.TaskID, taskType: expectation.TaskType}] = index
	}
	pairs := make([]MCPReceiptPair, len(expectations))
	taskIndices := make(map[uint64]int, len(expectations))
	for _, event := range events {
		if event.Sequence <= cursor.Sequence {
			continue
		}
		identity := mcpReceiptIdentity{dashboardGeneration: event.DashboardGeneration, gateGeneration: event.GateGeneration, serverID: event.ServerID, taskID: event.TaskID, taskType: event.TaskType}
		index, expected := indices[identity]
		if !expected {
			return nil, false, fmt.Errorf("unexpected MCP receipt event after cursor: %+v", event)
		}
		switch event.Kind {
		case MCPReceiptTask:
			if _, duplicate := taskIndices[event.TaskID]; duplicate || pairs[index].Task.TaskID != 0 {
				return nil, false, fmt.Errorf("duplicate MCP task receipt for server %d task type %d: %+v", event.ServerID, event.TaskType, event)
			}
			taskIndices[event.TaskID] = index
			pairs[index].Task = event
		case MCPReceiptResult:
			taskIndex, exists := taskIndices[event.TaskID]
			if !exists {
				return nil, false, fmt.Errorf("MCP result receipt has no matching task: %+v", event)
			}
			if taskIndex != index || pairs[index].Result.TaskID != 0 {
				return nil, false, fmt.Errorf("duplicate or mismatched MCP result receipt: %+v", event)
			}
			task := pairs[index].Task
			if event.ServerID != task.ServerID || event.TaskType != task.TaskType || event.GateGeneration != task.GateGeneration || event.DashboardGeneration != task.DashboardGeneration || event.TaskID != task.TaskID {
				return nil, false, fmt.Errorf("MCP result receipt does not match task: task=%+v result=%+v", task, event)
			}
			pairs[index].Result = event
		default:
			return nil, false, fmt.Errorf("unexpected MCP receipt kind %q: %+v", event.Kind, event)
		}
	}
	for _, pair := range pairs {
		if pair.Task.TaskID == 0 || pair.Result.TaskID == 0 {
			return nil, false, nil
		}
	}
	return pairs, true, nil
}
