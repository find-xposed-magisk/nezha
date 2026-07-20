//go:build linux

package dashboard

import (
	"context"
	"errors"
	"fmt"
)

func (dashboard *Dashboard) MCPReceiptCursor() MCPReceiptCursor {
	dashboard.eventMu.RLock()
	defer dashboard.eventMu.RUnlock()
	return MCPReceiptCursor{Sequence: dashboard.mcpReceiptSequence}
}

func (dashboard *Dashboard) MCPReceiptEventsAfter(cursor MCPReceiptCursor) []MCPReceiptEvent {
	dashboard.eventMu.RLock()
	defer dashboard.eventMu.RUnlock()
	events := make([]MCPReceiptEvent, 0, len(dashboard.mcpReceiptEvents))
	for _, event := range dashboard.mcpReceiptEvents {
		if event.Sequence > cursor.Sequence {
			events = append(events, event)
		}
	}
	return events
}

func (dashboard *Dashboard) WaitForMCPReceiptPairs(ctx context.Context, cursor MCPReceiptCursor, expectations []MCPReceiptExpectation) ([]MCPReceiptPair, error) {
	if len(expectations) == 0 {
		return nil, errors.New("MCP receipt expectations are empty")
	}
	for {
		dashboard.eventMu.RLock()
		notify, closed := dashboard.eventNotify, dashboard.eventClosed
		events := append([]MCPReceiptEvent(nil), dashboard.mcpReceiptEvents...)
		dashboard.eventMu.RUnlock()
		pairs, complete, err := matchMCPReceiptPairs(events, cursor, expectations)
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

func matchMCPReceiptPairs(events []MCPReceiptEvent, cursor MCPReceiptCursor, expectations []MCPReceiptExpectation) ([]MCPReceiptPair, bool, error) {
	pairs := make([]MCPReceiptPair, len(expectations))
	matched := make(map[uint64]int, len(expectations))
	for _, event := range events {
		if event.Sequence <= cursor.Sequence {
			continue
		}
		index, exists := matched[event.TaskID]
		if !exists {
			if event.Kind != MCPReceiptTask || len(matched) >= len(expectations) {
				return nil, false, fmt.Errorf("unexpected MCP receipt event after cursor: %+v", event)
			}
			index = len(matched)
			expectation := expectations[index]
			if event.ServerID != expectation.ServerID || event.TaskType != expectation.TaskType {
				return nil, false, fmt.Errorf("MCP task receipt mismatch at index %d: %+v", index, event)
			}
			matched[event.TaskID] = index
			pairs[index].Task = event
			continue
		}
		if event.Kind != MCPReceiptResult || pairs[index].Result.TaskID != 0 {
			return nil, false, fmt.Errorf("MCP task ID %d was received more than once", event.TaskID)
		}
		if event.ServerID != pairs[index].Task.ServerID || event.TaskType != pairs[index].Task.TaskType || event.GateGeneration != pairs[index].Task.GateGeneration || event.DashboardGeneration != pairs[index].Task.DashboardGeneration {
			return nil, false, fmt.Errorf("MCP result receipt does not match task: task=%+v result=%+v", pairs[index].Task, event)
		}
		pairs[index].Result = event
	}
	if len(matched) != len(expectations) {
		return nil, false, nil
	}
	for _, pair := range pairs {
		if pair.Result.TaskID == 0 {
			return nil, false, nil
		}
	}
	return pairs, true, nil
}
