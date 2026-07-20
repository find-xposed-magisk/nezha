//go:build linux

package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

type RegistrationConfigExecInput struct {
	Paths contract.Paths
	Fault contract.Fault
}

type RegistrationConfigExec struct{}

type serverListArguments struct {
	OnlineOnly bool `json:"online_only"`
}
type serverListResult struct {
	Servers []struct {
		ID     uint64 `json:"id"`
		UUID   string `json:"uuid"`
		Online bool   `json:"online"`
	} `json:"servers"`
}
type serverGetArguments struct {
	ServerID uint64 `json:"server_id"`
}
type serverGetResult struct {
	UUID  string          `json:"uuid"`
	Host  json.RawMessage `json:"host"`
	State json.RawMessage `json:"state"`
}
type execArguments struct {
	ServerID uint64   `json:"server_id"`
	Cmd      string   `json:"cmd"`
	Args     []string `json:"args"`
}
type execResult struct {
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	TimedOut        bool   `json:"timed_out"`
	Error           string `json:"error"`
}
type configPostRequest struct {
	Servers []uint64 `json:"servers"`
	Config  string   `json:"config"`
}
type configPostResponse struct {
	Success []uint64 `json:"success"`
	Failure []uint64 `json:"failure"`
	Offline []uint64 `json:"offline"`
}
type patRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expires_in_days"`
}
type patResponse struct {
	Token string `json:"token"`
}

func (RegistrationConfigExec) Run(ctx context.Context, input RegistrationConfigExecInput) (result Result, runErr error) {
	assertions := NewAssertionSet()
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: input.Paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		return Result{Name: "registration-config-exec", Assertions: assertions.Results(), Error: err.Error()}, err
	}
	defer func() {
		cleanupErr := dashboardInstance.Stop(context.Background())
		result.CleanupOK = cleanupErr == nil && dashboardInstance.CleanupReceipt().Passed
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
			result.Passed = false
			result.Error = errorText(cleanupErr)
		}
	}()

	secret := dashboardInstance.AgentSecret()
	agentSecret := secret
	if input.Fault.String() == "agent-bad-secret" {
		agentSecret = "wrong-agent-secret"
	}
	agentInstance, err := agent.Start(ctx, agent.AgentStartConfig{SourceDir: input.Paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(), Secret: agentSecret, UUID: "00000000-0000-0000-0000-000000000111"})
	if err != nil {
		return Result{Name: "registration-config-exec", Assertions: assertions.Results(), Error: err.Error()}, err
	}
	defer func() {
		cleanupErr := agentInstance.Stop(context.Background())
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
			result.Passed = false
			result.Error = errorText(cleanupErr)
		}
	}()
	if input.Fault.String() == "agent-bad-secret" {
		badContext, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		err = agentInstance.AssertNeverOnline(badContext, dashboardInstance, 3*time.Second)
		faultDetails := "invalid secret prevented readiness as expected"
		if err != nil {
			faultDetails = errorText(err)
		}
		assertions.Record("agent-bad-secret prevents readiness", false, faultDetails)
		if err == nil {
			err = errors.New("fault injection agent-bad-secret")
		}
		return finish(assertions, err)
	}
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return finish(assertions, err)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return finish(assertions, err)
	}
	readiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	assertions.Record("online inventory has exact UUID", err == nil && readiness.UUID == agentInstance.UUID() && readiness.Online, errorText(err))
	assertions.Record("online inventory has Host and State", err == nil && len(readiness.Host) > 0 && len(readiness.State) > 0, errorText(err))
	if err != nil {
		return finish(assertions, err)
	}

	servers, err := client.CallTool[serverListArguments, serverListResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}})
	if err != nil {
		return finish(assertions, err)
	}
	serverID := uint64(0)
	for _, server := range servers.StructuredContent.Servers {
		if server.UUID == agentInstance.UUID() && server.Online {
			serverID = server.ID
		}
	}
	assertions.Record("server.list exact online UUID", serverID != 0, "")
	if serverID == 0 {
		return finish(assertions, errors.New("server.list did not return the agent UUID"))
	}
	server, err := client.CallTool[serverGetArguments, serverGetResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[serverGetArguments]{Name: "server.get", Arguments: serverGetArguments{ServerID: serverID}})
	assertions.Record("server.get exact UUID and meaningful Host State", err == nil && server.StructuredContent.UUID == agentInstance.UUID() && string(server.StructuredContent.Host) != "null" && string(server.StructuredContent.State) != "null", errorText(err))
	if err != nil {
		return finish(assertions, err)
	}

	limited, err := createScopedClient(ctx, dashboardInstance, []string{"nezha:server:read"})
	if err != nil {
		return finish(assertions, err)
	}
	_, err = client.DoREST[struct{}, string](ctx, limited, client.RESTRequest[struct{}]{Method: http.MethodGet, Path: fmt.Sprintf("/api/v1/server/config/%d", serverID)})
	assertions.Record("insufficient config scope denied", isForbidden(err), errorText(err))
	configRaw, err := client.DoREST[struct{}, string](ctx, dashboardInstance.Clients().REST, client.RESTRequest[struct{}]{Method: http.MethodGet, Path: fmt.Sprintf("/api/v1/server/config/%d", serverID)})
	if err != nil {
		return finish(assertions, err)
	}
	original, err := decodeAgentConfig(configRaw)
	assertions.Record("authorized config returns complete round-trip contract", err == nil && original.ClientSecret != "" && original.UUID == agentInstance.UUID() && original.Server != "", errorText(err))
	if err != nil {
		return finish(assertions, err)
	}
	updated := original
	updated.Debug = !original.Debug
	updated.ReportDelay = original.ReportDelay%4 + 1
	configDiffErr := changedOnlyDebugAndReportDelay(original, updated)
	assertions.Record("config diff changes only debug and report_delay", configDiffErr == nil, errorText(configDiffErr))
	if configDiffErr != nil {
		return finish(assertions, configDiffErr)
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return finish(assertions, err)
	}
	response, err := client.DoREST[configPostRequest, configPostResponse](ctx, dashboardInstance.Clients().REST, client.RESTRequest[configPostRequest]{Method: http.MethodPost, Path: "/api/v1/server/config", Body: &configPostRequest{Servers: []uint64{serverID}, Config: string(encoded)}})
	dispatchValid := err == nil && len(response.Success) == 1 && response.Success[0] == serverID
	dispatchDetails := errorText(err)
	if !dispatchValid && dispatchDetails == "" {
		dispatchDetails = fmt.Sprintf("success=%v failure=%v offline=%v", response.Success, response.Failure, response.Offline)
	}
	assertions.Record("config update dispatched", dispatchValid, dispatchDetails)
	if !dispatchValid {
		if err != nil {
			return finish(assertions, fmt.Errorf("config dispatch failed: %w", err))
		}
		return finish(assertions, errors.New("config dispatch returned no successful server"))
	}
	// Agent ApplyConfig commits after its deferred reload window, then reconnects;
	// this state-generation event is the harness boundary that proves the new
	// connection published state instead of merely accepting the task.
	stateGeneration := dashboardInstance.StateGeneration(serverID, agentInstance.UUID())
	if stateGeneration == 0 {
		return finish(assertions, errors.New("state generation was not observed before config reload"))
	}
	if err := dashboardInstance.WaitForStateGeneration(ctx, serverID, agentInstance.UUID(), stateGeneration+1, 1); err != nil {
		return finish(assertions, err)
	}
	persisted, err := waitForPersistedConfig(ctx, agentInstance.ConfigPath(), updated)
	if err != nil {
		return finish(assertions, err)
	}
	persistedMatches := persisted.Debug == updated.Debug && persisted.ReportDelay == updated.ReportDelay && persisted.ClientSecret == original.ClientSecret && persisted.UUID == original.UUID && persisted.Server == original.Server
	assertions.Record("config reload persisted only requested changes", persistedMatches, fmt.Sprintf("debug=%t/%t report_delay=%d/%d uuid=%s/%s server=%s/%s", persisted.Debug, updated.Debug, persisted.ReportDelay, updated.ReportDelay, persisted.UUID, original.UUID, persisted.Server, original.Server))
	postReload, err := agentInstance.WaitReady(ctx, dashboardInstance)
	assertions.Record("post-reload online identity remains stable", err == nil && postReload.UUID == agentInstance.UUID() && postReload.Online, errorText(err))
	if err != nil {
		return finish(assertions, err)
	}

	exec, err := client.CallTool[execArguments, execResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[execArguments]{Name: "server.exec", Arguments: execArguments{ServerID: serverID, Cmd: "/bin/sh", Args: []string{"-c", "printf compat-exec"}}})
	assertions.Record("valid Exec exact stdout exit and no truncation timeout", err == nil && exec.StructuredContent.ExitCode == 0 && exec.StructuredContent.Stdout == "compat-exec" && exec.StructuredContent.Error == "" && !exec.StructuredContent.StdoutTruncated && !exec.StructuredContent.TimedOut, errorText(err))
	_, invalidErr := client.CallTool[execArguments, execResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[execArguments]{Name: "server.exec", Arguments: execArguments{ServerID: serverID, Cmd: "/definitely/missing/compat-command"}})
	var toolFailure *client.ToolFailure
	structuredFailure := errors.As(invalidErr, &toolFailure)
	var invalidResult execResult
	if structuredFailure {
		decodeErr := json.Unmarshal(toolFailure.StructuredContent, &invalidResult)
		structuredFailure = decodeErr == nil
		if decodeErr != nil {
			invalidErr = errors.Join(invalidErr, decodeErr)
		}
	}
	assertions.Record("invalid Exec has typed nonzero semantics", structuredFailure && invalidResult.ExitCode != 0 && invalidResult.Error != "", errorText(invalidErr))
	_, err = client.CallTool[serverListArguments, serverListResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}})
	assertions.Record("MCP health continues after Exec", err == nil, errorText(err))
	return finish(assertions, nil)
}

func waitForPersistedConfig(ctx context.Context, path string, want AgentConfig) (AgentConfig, error) {
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		config, err := ReadConfigFile(path)
		if err == nil && config.Debug == want.Debug && config.ReportDelay == want.ReportDelay {
			return config, nil
		}
		select {
		case <-ticker.C:
		case <-deadline.Done():
			if err != nil {
				return AgentConfig{}, fmt.Errorf("wait for persisted config: %w", err)
			}
			return AgentConfig{}, fmt.Errorf("wait for persisted config: %w", deadline.Err())
		}
	}
}

func finish(assertions *AssertionSet, runErr error) (Result, error) {
	for _, assertion := range assertions.assertions {
		if !assertion.Passed && runErr == nil {
			runErr = fmt.Errorf("%s: %s", assertion.Name, assertion.Details)
		}
	}
	result := Result{Name: "registration-config-exec", Passed: runErr == nil, Assertions: assertions.Results(), CleanupOK: false}
	if runErr != nil {
		result.Error = evidence.Redact(runErr.Error())
	}
	return result, runErr
}

func createScopedClient(ctx context.Context, dashboardInstance *dashboard.Dashboard, scopes []string) (*client.Client, error) {
	pat, err := client.DoREST[patRequest, patResponse](ctx, dashboardInstance.Clients().REST, client.RESTRequest[patRequest]{Method: http.MethodPost, Path: "/api/v1/api-tokens", Body: &patRequest{Name: "agentcompat-scope-check", Scopes: scopes}})
	if err != nil {
		return nil, err
	}
	return dashboardInstance.AuthenticatedClient(pat.Token)
}

func isForbidden(err error) bool {
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusForbidden
	}
	var handshakeErr *client.WebSocketHandshakeError
	return errors.As(err, &handshakeErr) && handshakeErr.StatusCode == http.StatusForbidden
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return evidence.Redact(err.Error())
}
