//go:build linux

package scenario

import "github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"

func reconnectReceiptSummary(pairs ...[]dashboard.MCPReceiptPair) (taskIDs, resultIDs []uint64, duplicates, lost int) {
	seen := make(map[uint64]struct{})
	for _, group := range pairs {
		for _, pair := range group {
			taskIDs = append(taskIDs, pair.Task.TaskID)
			resultIDs = append(resultIDs, pair.Result.TaskID)
			if _, exists := seen[pair.Task.TaskID]; exists {
				duplicates++
			}
			seen[pair.Task.TaskID] = struct{}{}
			if pair.Task.TaskID == 0 || pair.Task.TaskID != pair.Result.TaskID {
				lost++
			}
		}
	}
	return taskIDs, resultIDs, duplicates, lost
}

func staleReconnectReceiptCount(generation uint64, pairs ...[]dashboard.MCPReceiptPair) int {
	count := 0
	for _, group := range pairs {
		for _, pair := range group {
			if pair.Task.DashboardGeneration == generation || pair.Result.DashboardGeneration == generation {
				count++
			}
		}
	}
	return count
}
