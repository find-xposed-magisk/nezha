//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	"github.com/nezhahq/nezha/model"
)

type reconnectExecArguments struct {
	ServerID uint64   `json:"server_id"`
	Cmd      string   `json:"cmd"`
	Args     []string `json:"args"`
}

type reconnectExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Error    string `json:"error"`
}

func runDashboardReconnectOperations(ctx context.Context, dashboardInstance *dashboard.Dashboard, serverID uint64, uuid, fixturePath string) ([]dashboard.MCPReceiptPair, error) {
	cursor := dashboardInstance.MCPReceiptCursor()
	server, err := client.CallTool[client.ServerGetArguments, client.ServerGetResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[client.ServerGetArguments]{Name: "server.get", Arguments: client.ServerGetArguments{ServerID: serverID}})
	if err != nil || server.StructuredContent.ID != serverID || server.StructuredContent.UUID != uuid || string(server.StructuredContent.Host) == "null" || string(server.StructuredContent.State) == "null" {
		return nil, errors.Join(errors.New("post-reconnect server.get identity mismatch"), err)
	}
	if err := runReconnectExec(ctx, dashboardInstance.Clients().MCP, serverID, "dashboard-reconnect"); err != nil {
		return nil, err
	}
	write, err := client.CallTool[client.FsWriteArguments, client.FsWriteResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[client.FsWriteArguments]{Name: "fs.write", Arguments: client.FsWriteArguments{ServerID: serverID, Path: fixturePath, Content: "dashboard-generation-two", Encoding: "utf8", Mode: "0600", CreateDirs: true}})
	if err != nil || write.StructuredContent.Size != int64(len("dashboard-generation-two")) || write.StructuredContent.Error != "" {
		return nil, errors.Join(errors.New("post-reconnect fs.write mismatch"), err)
	}
	if err := runReconnectRead(ctx, dashboardInstance.Clients().MCP, serverID, fixturePath, "dashboard-generation-two"); err != nil {
		return nil, err
	}
	generation := dashboardInstance.RuntimeIdentity().Generation
	expectations, err := reconnectReceiptExpectations(dashboardInstance.MCPReceiptEventsAfter(cursor), generation, serverID, []uint64{model.TaskTypeExec, model.TaskTypeFsWrite, model.TaskTypeFsRead})
	if err != nil {
		return nil, err
	}
	return dashboardInstance.WaitForMCPReceiptSet(ctx, cursor, expectations)
}

func runAgentRestartOperations(ctx context.Context, dashboardInstance *dashboard.Dashboard, serverID uint64, fixturePath string) ([]dashboard.MCPReceiptPair, error) {
	cursor := dashboardInstance.MCPReceiptCursor()
	if err := runReconnectExec(ctx, dashboardInstance.Clients().MCP, serverID, "agent-restart"); err != nil {
		return nil, err
	}
	if err := runReconnectRead(ctx, dashboardInstance.Clients().MCP, serverID, fixturePath, "dashboard-generation-two"); err != nil {
		return nil, err
	}
	generation := dashboardInstance.RuntimeIdentity().Generation
	expectations, err := reconnectReceiptExpectations(dashboardInstance.MCPReceiptEventsAfter(cursor), generation, serverID, []uint64{model.TaskTypeExec, model.TaskTypeFsRead})
	if err != nil {
		return nil, err
	}
	return dashboardInstance.WaitForMCPReceiptSet(ctx, cursor, expectations)
}

func reconnectReceiptExpectations(events []dashboard.MCPReceiptEvent, generation, serverID uint64, taskTypes []uint64) ([]dashboard.MCPReceiptExpectation, error) {
	pending := append([]uint64(nil), taskTypes...)
	expectations := make([]dashboard.MCPReceiptExpectation, 0, len(pending))
	for _, event := range events {
		if event.Kind != dashboard.MCPReceiptTask || event.DashboardGeneration != generation || event.ServerID != serverID {
			continue
		}
		for index, taskType := range pending {
			if taskType == event.TaskType {
				expectations = append(expectations, dashboard.MCPReceiptExpectation{DashboardGeneration: event.DashboardGeneration, GateGeneration: event.GateGeneration, ServerID: event.ServerID, TaskID: event.TaskID, TaskType: event.TaskType})
				pending = append(pending[:index], pending[index+1:]...)
				break
			}
		}
	}
	if len(pending) != 0 {
		return nil, errors.New("reconnect receipt task set is incomplete")
	}
	return expectations, nil
}

func runReconnectExec(ctx context.Context, mcpClient *client.Client, serverID uint64, marker string) error {
	result, err := client.CallTool[reconnectExecArguments, reconnectExecResult](ctx, mcpClient, client.ToolCall[reconnectExecArguments]{Name: "server.exec", Arguments: reconnectExecArguments{ServerID: serverID, Cmd: "/bin/sh", Args: []string{"-c", "printf " + marker}}})
	if err != nil {
		return err
	}
	if result.StructuredContent.ExitCode != 0 || result.StructuredContent.Stdout != marker || result.StructuredContent.Stderr != "" || result.StructuredContent.Error != "" {
		return fmt.Errorf("reconnect Exec mismatch: %+v", result.StructuredContent)
	}
	return nil
}

func runReconnectRead(ctx context.Context, mcpClient *client.Client, serverID uint64, path, expected string) error {
	result, err := client.CallTool[client.FsReadArguments, client.FsReadResult](ctx, mcpClient, client.ToolCall[client.FsReadArguments]{Name: "fs.read", Arguments: client.FsReadArguments{ServerID: serverID, Path: path, Encoding: "utf8"}})
	if err != nil {
		return err
	}
	if result.StructuredContent.Content != expected || result.StructuredContent.Encoding != "utf8" || result.StructuredContent.Size != int64(len(expected)) || result.StructuredContent.Truncated {
		return fmt.Errorf("reconnect fs.read mismatch: %+v", result.StructuredContent)
	}
	return nil
}

func prepareReconnectSentinel(agentRoot string) (fixturePath, sentinelPath string, err error) {
	root, err := fixture.NewAgentRoot(agentRoot, "reconnect-files")
	if err != nil {
		return "", "", err
	}
	path, err := root.Path("runtime.txt")
	if err != nil {
		return "", "", err
	}
	fixturePath = path.String()
	sentinelPath = agentRoot + "/outside-reconnect-sentinel"
	err = os.WriteFile(sentinelPath, []byte("outside-reconnect-root-sentinel"), 0o600)
	return fixturePath, sentinelPath, err
}
