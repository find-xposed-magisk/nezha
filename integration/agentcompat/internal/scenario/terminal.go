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
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

const (
	terminalMarker                = "compat-terminal"
	terminalCommand               = "printf 'compat-size='; stty size; printf 'compat-terminal\\n'; exit\n"
	terminalShutdownContract      = 2 * time.Second
	terminalShutdownHarnessMargin = 500 * time.Millisecond
	terminalAttachPATScope        = "nezha:server:exec"
)

type TerminalInput struct {
	Paths contract.Paths
	Fault contract.Fault
}

type Terminal struct{}

type terminalCreateRequest struct {
	Protocol string `json:"protocol"`
	ServerID uint64 `json:"server_id"`
}

type terminalCreateResponse struct {
	SessionID string `json:"session_id"`
	ServerID  uint64 `json:"server_id"`
}

type terminalUserRequest struct {
	Role     uint8  `json:"role"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type terminalPATRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ServerIDs []uint64 `json:"server_ids,omitempty"`
}

type terminalPATResponse struct {
	Token string `json:"token"`
}

func (Terminal) Run(ctx context.Context, input TerminalInput) (result Result, runErr error) {
	assertions := NewAssertionSet()
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: input.Paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		return terminalFinish(assertions, err)
	}
	result.CleanupOK = true
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		cleanupErr := dashboardInstance.Stop(cleanupContext)
		receipt := dashboardInstance.CleanupReceipt()
		cleanupPassed := cleanupErr == nil && receipt.Passed && !receipt.Forced
		result.CleanupOK = result.CleanupOK && cleanupPassed
		if !cleanupPassed {
			cleanupErr = errors.Join(cleanupErr, errors.New("dashboard cleanup receipt failed"))
			result, runErr = terminalFinish(assertions, errors.Join(runErr, cleanupErr))
			result.CleanupOK = false
		}
	}()

	agentInstance, err := agent.Start(ctx, agent.AgentStartConfig{SourceDir: input.Paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(), Secret: dashboardInstance.AgentSecret(), UUID: "00000000-0000-0000-0000-000000000113"})
	if err != nil {
		return terminalFinish(assertions, err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		cleanupErr := agentInstance.Stop(cleanupContext)
		receipt := agentInstance.CleanupReceipt()
		cleanupPassed := cleanupErr == nil && receipt.Passed && !receipt.Forced
		result.CleanupOK = result.CleanupOK && cleanupPassed
		if !cleanupPassed {
			cleanupErr = errors.Join(cleanupErr, errors.New("agent cleanup receipt failed"))
			result, runErr = terminalFinish(assertions, errors.Join(runErr, cleanupErr))
			result.CleanupOK = false
		}
	}()
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return terminalFinish(assertions, err)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return terminalFinish(assertions, err)
	}
	readiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	if err != nil {
		return terminalFinish(assertions, err)
	}
	serverID, err := terminalServerID(ctx, dashboardInstance.Clients().MCP, readiness.UUID)
	if err != nil {
		return terminalFinish(assertions, err)
	}
	baseline, err := processharness.SampleProcess(agentInstance.PID())
	if err != nil {
		return terminalFinish(assertions, err)
	}

	deniedClient, err := createTerminalPATClient(ctx, dashboardInstance, "terminal-denied", []string{"nezha:server:read"}, []uint64{serverID})
	if err != nil {
		return terminalFinish(assertions, err)
	}
	_, deniedErr := client.DoREST[terminalCreateRequest, terminalCreateResponse](ctx, deniedClient, client.RESTRequest[terminalCreateRequest]{Method: http.MethodPost, Path: "/api/v1/terminal", Body: &terminalCreateRequest{Protocol: "grpc", ServerID: serverID}})
	assertions.Record("terminal denied PAT lacks exec scope", isForbidden(deniedErr), errorText(deniedErr))

	terminal, err := client.DoREST[terminalCreateRequest, terminalCreateResponse](ctx, dashboardInstance.Clients().REST, client.RESTRequest[terminalCreateRequest]{Method: http.MethodPost, Path: "/api/v1/terminal", Body: &terminalCreateRequest{Protocol: "grpc", ServerID: serverID}})
	if err != nil || terminal.SessionID == "" || terminal.ServerID != serverID {
		return terminalFinish(assertions, errors.Join(err, errors.New("terminal creation returned incomplete session")))
	}
	foreignClient, cleanupForeignUser, err := createForeignTerminalPATClient(ctx, dashboardInstance)
	if err != nil {
		return terminalFinish(assertions, err)
	}
	hijackConnection, hijackErr := foreignClient.DialWebSocket(ctx, "/api/v1/ws/terminal/"+terminal.SessionID)
	if hijackConnection != nil {
		_ = hijackConnection.Close()
	}
	assertions.Record("foreign scoped PAT cannot hijack terminal session", isWebSocketDenied(hijackErr), fmt.Sprintf("scopes=[%s] denial=%s", terminalAttachPATScope, errorText(hijackErr)))
	if err := cleanupForeignUser(); err != nil {
		return terminalFinish(assertions, err)
	}

	connection, err := dashboardInstance.Clients().WebSocket.DialWebSocket(ctx, "/api/v1/ws/terminal/"+terminal.SessionID)
	if err != nil {
		return terminalFinish(assertions, err)
	}
	defer connection.Close()
	if err := connection.WriteFrame(ctx, mustTerminalResizeFrame(132, 43)); err != nil {
		return terminalFinish(assertions, err)
	}
	initialFrame, err := connection.ReadFrame(ctx)
	if err != nil {
		return terminalFinish(assertions, err)
	}
	active, err := processharness.SampleProcess(agentInstance.PID())
	if err != nil {
		return terminalFinish(assertions, err)
	}
	// Start the contract clock before sending exit so transport time is included.
	exitSentAt := time.Now()
	output, err := executeTerminalExit(ctx, terminalExitInput{InitialOutput: initialFrame.Payload, ExitSentAt: exitSentAt, Now: time.Now}, connection)
	assertions.Record("terminal resize marker and bounded shell close observed", err == nil && output.MarkerObserved && output.SizeObserved && output.Rows == 43 && output.Cols == 132 && output.StreamClosed && terminalCloseWithinContract(output.CloseElapsed), terminalOutputDetails(output, err))
	if err != nil {
		return terminalFinish(assertions, err)
	}
	residue, err := processharness.SampleProcess(agentInstance.PID())
	residueClean := err == nil && active.DescendantCount > baseline.DescendantCount && residue.DescendantCount == baseline.DescendantCount && residue.TCPListenerCount == baseline.TCPListenerCount && residue.TCP6ListenerCount == baseline.TCP6ListenerCount
	assertions.Record("agent PTY child and listener residue cleared", residueClean, fmt.Sprintf("active_children=%d baseline_children=%d residue_children=%d baseline_listeners=%d/%d residue_listeners=%d/%d error=%s", active.DescendantCount, baseline.DescendantCount, residue.DescendantCount, baseline.TCPListenerCount, baseline.TCP6ListenerCount, residue.TCPListenerCount, residue.TCP6ListenerCount, errorText(err)))
	_, staleErr := dashboardInstance.Clients().WebSocket.DialWebSocket(ctx, "/api/v1/ws/terminal/"+terminal.SessionID)
	assertions.Record("terminal IOStream removed after shell exit", isWebSocketDenied(staleErr), errorText(staleErr))
	_, invalidErr := dashboardInstance.Clients().WebSocket.DialWebSocket(ctx, "/api/v1/ws/terminal/invalid-session")
	assertions.Record("invalid terminal session rejected", webSocketFailureContains(invalidErr, "permission denied"), errorText(invalidErr))
	return terminalFinish(assertions, nil)
}

func terminalResizeFrame(cols, rows uint32) (client.Frame, error) {
	payload, err := json.Marshal(struct {
		Cols uint32
		Rows uint32
	}{Cols: cols, Rows: rows})
	if err != nil {
		return client.Frame{}, err
	}
	return client.Frame{Type: client.FrameBinary, Payload: append([]byte{1}, payload...)}, nil
}

func mustTerminalResizeFrame(cols, rows uint32) client.Frame {
	frame, _ := terminalResizeFrame(cols, rows)
	return frame
}

func terminalFinish(assertions *AssertionSet, runErr error) (Result, error) {
	results := assertions.Results()
	failedAssertion := false
	for _, assertion := range results {
		if !assertion.Passed && runErr == nil {
			runErr = fmt.Errorf("%s: %s", assertion.Name, assertion.Details)
		}
		failedAssertion = failedAssertion || !assertion.Passed
	}
	if runErr != nil && !failedAssertion {
		results = append(results, Assertion{Name: "terminal scenario completed", Passed: false, Details: evidence.Redact(runErr.Error())})
	}
	result := Result{Name: "terminal", Passed: runErr == nil, Assertions: results, CleanupOK: true}
	if runErr != nil {
		result.Error = evidence.Redact(runErr.Error())
	}
	return result, runErr
}
