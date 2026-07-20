//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

const readinessBudget = 45 * time.Second

type Readiness struct {
	ServerID               uint64
	UUID                   string
	Version                string
	Online                 bool
	LastActive             time.Time
	VersionObserved        bool
	RequestTaskEstablished bool
	StateReceiptObserved   bool
	Host                   json.RawMessage
	State                  json.RawMessage
}

type serverListArguments struct {
	OnlineOnly bool `json:"online_only"`
}

type serverListResult struct {
	Servers []serverListItem `json:"servers"`
	Count   int              `json:"count"`
}

type serverListItem struct {
	ID         uint64    `json:"id"`
	UUID       string    `json:"uuid"`
	Online     bool      `json:"online"`
	Platform   string    `json:"platform"`
	Arch       string    `json:"arch"`
	LastActive time.Time `json:"last_active"`
}

type serverGetArguments struct {
	ServerID uint64 `json:"server_id"`
}

type serverGetResult struct {
	ID         uint64          `json:"id"`
	UUID       string          `json:"uuid"`
	Host       json.RawMessage `json:"host"`
	State      json.RawMessage `json:"state"`
	LastActive time.Time       `json:"last_active"`
}

type execArguments struct {
	ServerID uint64   `json:"server_id"`
	Cmd      string   `json:"cmd"`
	Args     []string `json:"args"`
}

type execResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Error    string `json:"error"`
}

func (agent *Agent) WaitReady(ctx context.Context, dashboardInstance *dashboard.Dashboard) (Readiness, error) {
	deadline, cancel := context.WithTimeout(ctx, readinessBudget)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		readiness, err := agent.probeReadiness(deadline, dashboardInstance)
		if err == nil {
			return readiness, nil
		}
		select {
		case <-agent.supervisor.Exited():
			return Readiness{}, fmt.Errorf("agent exited before readiness: %w", err)
		case <-deadline.Done():
			return Readiness{}, fmt.Errorf("agent readiness: %w", errors.Join(err, deadline.Err()))
		case <-ticker.C:
		}
	}
}

func (agent *Agent) WaitReadyEventDriven(ctx context.Context, dashboardInstance *dashboard.Dashboard) (Readiness, error) {
	serverID, err := dashboardInstance.WaitForInfo2UUID(ctx, agent.uuid)
	if err != nil {
		return Readiness{}, fmt.Errorf("agent info2 readiness: %w", err)
	}
	readiness, err := agent.probeReadinessForServer(ctx, dashboardInstance.Clients().MCP, serverID)
	if err != nil {
		return Readiness{}, err
	}
	return readiness, nil
}

func (agent *Agent) WaitReadyEventDrivenWithClient(ctx context.Context, dashboardInstance *dashboard.Dashboard, mcpClient *client.Client) (Readiness, error) {
	serverID, err := dashboardInstance.WaitForInfo2UUID(ctx, agent.uuid)
	if err != nil {
		return Readiness{}, fmt.Errorf("agent info2 readiness: %w", err)
	}
	return agent.probeReadinessForServer(ctx, mcpClient, serverID)
}

func (agent *Agent) probeReadinessForServer(ctx context.Context, mcpClient *client.Client, serverID uint64) (Readiness, error) {
	serverResponse, err := client.CallTool[serverGetArguments, serverGetResult](ctx, mcpClient, client.ToolCall[serverGetArguments]{Name: "server.get", Arguments: serverGetArguments{ServerID: serverID}})
	if err != nil {
		return Readiness{}, err
	}
	server := serverListItem{ID: serverID, UUID: agent.uuid, Online: true}
	if err := verifyServerGetResult(server, serverResponse.StructuredContent); err != nil {
		return Readiness{}, err
	}
	execResponse, err := client.CallTool[execArguments, execResult](ctx, mcpClient, client.ToolCall[execArguments]{Name: "server.exec", Arguments: execArguments{ServerID: serverID, Cmd: "sh", Args: []string{"-c", "printf agentcompat-ready"}}})
	if err != nil {
		return Readiness{}, fmt.Errorf("live RequestTask probe: %w", err)
	}
	if execResponse.StructuredContent.ExitCode != 0 || execResponse.StructuredContent.Stdout != "agentcompat-ready" {
		return Readiness{}, errors.New("live RequestTask probe returned unexpected result")
	}
	version, versionObserved, err := decodeHostVersionEvidence(serverResponse.StructuredContent.Host)
	if err != nil {
		return Readiness{}, err
	}
	return Readiness{ServerID: serverID, UUID: agent.uuid, Version: version, Online: true, LastActive: serverResponse.StructuredContent.LastActive, VersionObserved: versionObserved, RequestTaskEstablished: true, StateReceiptObserved: true, Host: serverResponse.StructuredContent.Host, State: serverResponse.StructuredContent.State}, nil
}

func (agent *Agent) probeReadiness(ctx context.Context, dashboardInstance *dashboard.Dashboard) (Readiness, error) {
	call := dashboardInstance.Clients().MCP
	list, err := client.CallTool[serverListArguments, serverListResult](ctx, call, client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}})
	if err != nil {
		return Readiness{}, err
	}
	var server serverListItem
	for _, candidate := range list.StructuredContent.Servers {
		if candidate.UUID == agent.uuid {
			server = candidate
			break
		}
	}
	if server.ID == 0 || server.UUID != agent.uuid || !server.Online {
		return Readiness{}, errors.New("agent UUID is not online in dashboard server.list")
	}
	execResponse, err := client.CallTool[execArguments, execResult](ctx, call, client.ToolCall[execArguments]{Name: "server.exec", Arguments: execArguments{ServerID: server.ID, Cmd: "sh", Args: []string{"-c", "printf agentcompat-ready"}}})
	if err != nil {
		return Readiness{}, fmt.Errorf("live RequestTask probe: %w", err)
	}
	if execResponse.StructuredContent.ExitCode != 0 || execResponse.StructuredContent.Stdout != "agentcompat-ready" {
		return Readiness{}, errors.New("live RequestTask probe returned unexpected result")
	}
	serverResponse, err := client.CallTool[serverGetArguments, serverGetResult](ctx, call, client.ToolCall[serverGetArguments]{Name: "server.get", Arguments: serverGetArguments{ServerID: server.ID}})
	if err != nil {
		return Readiness{}, err
	}
	if err := verifyServerGetResult(server, serverResponse.StructuredContent); err != nil {
		return Readiness{}, err
	}
	version, versionObserved, err := decodeHostVersionEvidence(serverResponse.StructuredContent.Host)
	if err != nil {
		return Readiness{}, err
	}
	stateReceiptObserved := dashboardInstance.ReceiptAccepted()
	if !dashboardInstance.ReceiptGateEnabled() {
		stateReceiptObserved = agent.observeStateReceipt(serverResponse.StructuredContent.LastActive)
	}
	if !stateReceiptObserved {
		return Readiness{}, errors.New("waiting for a second state report after receipt")
	}
	return Readiness{
		ServerID: server.ID, UUID: agent.uuid, Version: version, Online: true, LastActive: serverResponse.StructuredContent.LastActive, VersionObserved: versionObserved,
		RequestTaskEstablished: true, StateReceiptObserved: stateReceiptObserved,
		Host: serverResponse.StructuredContent.Host, State: serverResponse.StructuredContent.State,
	}, nil
}

func decodeHostVersionEvidence(raw json.RawMessage) (string, bool, error) {
	var host struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &host); err != nil {
		return "", false, fmt.Errorf("decode dashboard Host: %w", err)
	}
	// A decoded Host object is not version evidence unless the Agent reported a value.
	return host.Version, host.Version != "", nil
}

func verifyServerGetResult(server serverListItem, result serverGetResult) error {
	if server.ID == 0 || result.ID != server.ID || result.UUID != server.UUID {
		return errors.New("dashboard server.get identity does not match server.list")
	}
	if len(result.Host) == 0 || len(result.State) == 0 || string(result.Host) == "null" || string(result.State) == "null" {
		return errors.New("dashboard server.get omitted Host or State")
	}
	return nil
}

func (agent *Agent) observeStateReceipt(lastActive time.Time) bool {
	agent.readinessMu.Lock()
	defer agent.readinessMu.Unlock()
	observed := !agent.lastStateReport.IsZero() && lastActive.After(agent.lastStateReport)
	if lastActive.After(agent.lastStateReport) {
		agent.lastStateReport = lastActive
	}
	return observed
}

func (agent *Agent) AssertNeverOnline(ctx context.Context, dashboardInstance *dashboard.Dashboard, duration time.Duration) error {
	deadline, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastError error
	for {
		list, err := client.CallTool[serverListArguments, serverListResult](deadline, dashboardInstance.Clients().MCP, client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}})
		if err == nil {
			for _, server := range list.StructuredContent.Servers {
				if server.UUID == agent.uuid {
					return errors.New("invalid-secret agent became online")
				}
			}
		} else if deadline.Err() == nil {
			lastError = err
		}
		select {
		case <-deadline.Done():
			if lastError != nil {
				return fmt.Errorf("server.list unavailable while asserting agent stayed offline: %w", lastError)
			}
			return nil
		case <-ticker.C:
		}
	}
}
