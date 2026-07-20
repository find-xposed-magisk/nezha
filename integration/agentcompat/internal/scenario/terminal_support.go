//go:build linux

package scenario

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

func terminalServerID(ctx context.Context, mcpClient *client.Client, uuid string) (uint64, error) {
	servers, err := client.CallTool[serverListArguments, serverListResult](ctx, mcpClient, client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}})
	if err != nil {
		return 0, err
	}
	for _, server := range servers.StructuredContent.Servers {
		if server.UUID == uuid && server.Online {
			return server.ID, nil
		}
	}
	return 0, errors.New("terminal agent server ID not found")
}

func createTerminalPATClient(ctx context.Context, dashboardInstance *dashboard.Dashboard, name string, scopes []string, serverIDs []uint64) (*client.Client, error) {
	pat, err := client.DoREST[terminalPATRequest, terminalPATResponse](ctx, dashboardInstance.Clients().REST, client.RESTRequest[terminalPATRequest]{Method: http.MethodPost, Path: "/api/v1/api-tokens", Body: &terminalPATRequest{Name: name, Scopes: scopes, ServerIDs: serverIDs}})
	if err != nil {
		return nil, err
	}
	return dashboardInstance.AuthenticatedClient(pat.Token)
}

func createForeignTerminalPATClient(ctx context.Context, dashboardInstance *dashboard.Dashboard) (*client.Client, func() error, error) {
	const username = "terminal-member"
	const password = "terminal-member-password"
	admin := dashboardInstance.Clients().REST
	userID, err := client.DoREST[terminalUserRequest, uint64](ctx, admin, client.RESTRequest[terminalUserRequest]{Method: http.MethodPost, Path: "/api/v1/user", Body: &terminalUserRequest{Role: 1, Username: username, Password: password}})
	if err != nil {
		return nil, func() error { return nil }, err
	}
	cleanup := func() error {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, cleanupErr := client.DoREST[[]uint64, struct{}](cleanupContext, admin, client.RESTRequest[[]uint64]{Method: http.MethodPost, Path: "/api/v1/batch-delete/user", Body: &[]uint64{userID}})
		return cleanupErr
	}
	member, err := client.New(client.Config{BaseURL: dashboardInstance.URL()})
	if err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	if _, err := member.Login(ctx, client.LoginRequest{Username: username, Password: password}); err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	pat, err := client.DoREST[terminalPATRequest, terminalPATResponse](ctx, member, client.RESTRequest[terminalPATRequest]{Method: http.MethodPost, Path: "/api/v1/api-tokens", Body: &terminalPATRequest{Name: "terminal-foreign", Scopes: []string{terminalAttachPATScope}}})
	if err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	foreign, err := dashboardInstance.AuthenticatedClient(pat.Token)
	if err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	return foreign, cleanup, nil
}

func isWebSocketDenied(err error) bool {
	var handshakeError *client.WebSocketHandshakeError
	return errors.As(err, &handshakeError) && (handshakeError.StatusCode == http.StatusForbidden || webSocketFailureContains(err, "permission denied") || webSocketFailureContains(err, "ApiErrorUnauthorized"))
}

func webSocketFailureContains(err error, text string) bool {
	var handshakeError *client.WebSocketHandshakeError
	return errors.As(err, &handshakeError) && strings.Contains(handshakeError.Message, text)
}
