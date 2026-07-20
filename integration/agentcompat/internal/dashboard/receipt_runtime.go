//go:build linux

package dashboard

import (
	"bufio"
	"fmt"
	"strings"
)

func (dashboard *Dashboard) readReceiptEvents(generation uint64, reader *bufio.Reader) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			dashboard.eventMu.Lock()
			dashboard.stateMu.Lock()
			active := dashboard.eventGeneration == generation
			dashboard.stateMu.Unlock()
			if !active {
				dashboard.eventMu.Unlock()
				return
			}
			dashboard.eventClosed = true
			close(dashboard.eventNotify)
			dashboard.eventMu.Unlock()
			return
		}
		dashboard.processReceiptLineForGeneration(generation, line)
		dashboard.eventMu.Lock()
		dashboard.stateMu.Lock()
		active := dashboard.eventGeneration == generation
		dashboard.stateMu.Unlock()
		if !active {
			dashboard.eventMu.Unlock()
			return
		}
		close(dashboard.eventNotify)
		dashboard.eventNotify = make(chan struct{})
		dashboard.eventMu.Unlock()
	}
}

func (dashboard *Dashboard) processReceiptLine(line string) {
	dashboard.processReceiptLineForGeneration(0, line)
}

func (dashboard *Dashboard) processReceiptLineForGeneration(generation uint64, line string) {
	if strings.HasPrefix(line, "info2 ") {
		fields := strings.Fields(line)
		if len(fields) == 4 {
			line = fmt.Sprintf("info2 %s %s\n", fields[2], fields[3])
		}
		dashboard.info2Mu.Lock()
		dashboard.info2Events[line] = struct{}{}
		dashboard.info2Mu.Unlock()
	}
	if strings.HasPrefix(line, "accepted ") {
		var serverID, receiptGeneration, stateGeneration, count uint64
		var uuid string
		if _, parseErr := fmt.Sscanf(line, "accepted %d %s %d %d %d", &serverID, &uuid, &receiptGeneration, &stateGeneration, &count); parseErr == nil {
			dashboard.receiptMu.Lock()
			dashboard.receiptAccepted = true
			dashboard.receiptAcceptedCount = count
			dashboard.receiptGeneration = receiptGeneration
			dashboard.receiptMu.Unlock()
			dashboard.stateMu.Lock()
			dashboard.stateEvents[stateEventIdentity{ServerID: serverID, UUID: uuid, Generation: stateGeneration, Count: count}] = struct{}{}
			dashboard.stateMu.Unlock()
		}
	}
	if strings.HasPrefix(line, "state ") {
		var serverID, generation, count uint64
		var uuid string
		if _, parseErr := fmt.Sscanf(line, "state %d %s %d %d", &serverID, &uuid, &generation, &count); parseErr == nil {
			dashboard.stateMu.Lock()
			dashboard.stateEvents[stateEventIdentity{ServerID: serverID, UUID: uuid, Generation: generation, Count: count}] = struct{}{}
			dashboard.stateMu.Unlock()
		}
	}
	if strings.HasPrefix(line, "task ") || strings.HasPrefix(line, "result ") {
		var kind string
		var gateGeneration, serverID, taskID, taskType uint64
		if _, parseErr := fmt.Sscanf(line, "%s %d %d %d %d", &kind, &gateGeneration, &serverID, &taskID, &taskType); parseErr == nil {
			dashboard.eventMu.Lock()
			dashboard.stateMu.Lock()
			if generation != 0 && dashboard.eventGeneration != generation {
				dashboard.stateMu.Unlock()
				dashboard.eventMu.Unlock()
				return
			}
			dashboard.mcpReceiptSequence++
			dashboard.mcpReceiptEvents = append(dashboard.mcpReceiptEvents, MCPReceiptEvent{
				Sequence: dashboard.mcpReceiptSequence, DashboardGeneration: generation, GateGeneration: gateGeneration,
				ServerID: serverID, TaskID: taskID, TaskType: taskType, Kind: MCPReceiptKind(kind),
			})
			dashboard.stateMu.Unlock()
			dashboard.eventMu.Unlock()
		}
	}
}
