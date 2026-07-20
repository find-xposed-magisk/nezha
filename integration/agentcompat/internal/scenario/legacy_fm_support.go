//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

type legacyFMUserForm struct {
	Role     uint8  `json:"role"`
	Username string `json:"username"`
	Password string `json:"password"`
}

const legacyFMForeignUsername = "agentcompat-fm-foreign"
const legacyFMForeignPassword = "agentcompat-fm-password" // #nosec G101 -- Ephemeral localhost integration fixture, not a production credential.

type legacyFMFrameWriter interface {
	WriteFrame(context.Context, client.Frame) error
}

type legacyFMCommandDispatcher struct {
	writer legacyFMFrameWriter
	root   fixture.AgentRoot
}

func (dispatcher legacyFMCommandDispatcher) list(ctx context.Context, relative string) error {
	path, err := dispatcher.root.DestructivePath(relative)
	if err != nil {
		return err
	}
	return dispatcher.writer.WriteFrame(ctx, client.Frame{Type: client.FrameBinary, Payload: buildLegacyFMList(path)})
}

func (dispatcher legacyFMCommandDispatcher) upload(ctx context.Context, relative string, size uint64) error {
	path, err := dispatcher.root.DestructivePath(relative)
	if err != nil {
		return err
	}
	return dispatcher.writer.WriteFrame(ctx, client.Frame{Type: client.FrameBinary, Payload: buildLegacyFMUpload(path, size)})
}

func (dispatcher legacyFMCommandDispatcher) download(ctx context.Context, relative string) error {
	path, err := dispatcher.root.DestructivePath(relative)
	if err != nil {
		return err
	}
	return dispatcher.writer.WriteFrame(ctx, client.Frame{Type: client.FrameBinary, Payload: buildLegacyFMDownload(path)})
}

func createLegacyFMSession(ctx context.Context, dashboardClient *client.Client, serverID uint64, capabilities ...client.IOStreamCapability) (string, error) {
	var capability client.IOStreamCapability
	if len(capabilities) > 0 {
		capability = capabilities[0]
	}
	response, err := client.DoREST[struct{}, struct {
		SessionID string `json:"session_id"`
	}](ctx, dashboardClient, client.RESTRequest[struct{}]{Method: http.MethodPost, Path: fmt.Sprintf("/api/v1/file?id=%d", serverID), IOStreamCapability: capability})
	if err != nil {
		return "", err
	}
	if response.SessionID == "" {
		return "", errors.New("FM session response omitted session id")
	}
	return response.SessionID, nil
}

func verifyLegacyFMMissingScopes(ctx context.Context, dashboardInstance *dashboard.Dashboard, serverID uint64, session string) error {
	incompleteScopeSets := [][]string{
		{"nezha:server:write", "nezha:server:delete"},
		{"nezha:server:read", "nezha:server:delete"},
		{"nezha:server:read", "nezha:server:write"},
	}
	var scopeChecksErr error
	for _, scopes := range incompleteScopeSets {
		limited, err := createScopedClient(ctx, dashboardInstance, scopes)
		if err != nil {
			scopeChecksErr = errors.Join(scopeChecksErr, err)
			continue
		}
		_, createErr := createLegacyFMSession(ctx, limited, serverID)
		_, attachErr := limited.DialWebSocket(ctx, "/api/v1/ws/file/"+session)
		if !isForbidden(createErr) || !isForbidden(attachErr) {
			scopeChecksErr = errors.Join(scopeChecksErr, createErr, attachErr, errors.New("incomplete FM scopes were accepted"))
		}
	}
	return scopeChecksErr
}

func findLegacyFMServerID(ctx context.Context, dashboardInstance *dashboard.Dashboard, uuid string) (uint64, error) {
	type serverListArguments struct {
		OnlineOnly bool `json:"online_only"`
	}
	type serverListResult struct {
		Servers []struct {
			ID   uint64 `json:"id"`
			UUID string `json:"uuid"`
		} `json:"servers"`
	}
	result, err := client.CallTool[serverListArguments, serverListResult](ctx, dashboardInstance.Clients().MCP, client.ToolCall[serverListArguments]{Name: "server.list", Arguments: serverListArguments{OnlineOnly: true}})
	if err != nil {
		return 0, err
	}
	for _, server := range result.StructuredContent.Servers {
		if server.UUID == uuid && server.ID != 0 {
			return server.ID, nil
		}
	}
	return 0, errors.New("server.list omitted online FM server")
}

func createForeignLegacyFMClient(ctx context.Context, dashboardInstance *dashboard.Dashboard) (*client.Client, func() error, error) {
	admin := dashboardInstance.Clients().REST
	userID, err := client.DoREST[legacyFMUserForm, uint64](ctx, admin, client.RESTRequest[legacyFMUserForm]{
		Method: http.MethodPost,
		Path:   "/api/v1/user",
		Body:   &legacyFMUserForm{Role: 1, Username: legacyFMForeignUsername, Password: legacyFMForeignPassword},
	})
	if err != nil {
		return nil, func() error { return nil }, err
	}
	cleanup := func() error {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, cleanupErr := client.DoREST[[]uint64, struct{}](cleanupContext, admin, client.RESTRequest[[]uint64]{Method: http.MethodPost, Path: "/api/v1/batch-delete/user", Body: &[]uint64{userID}})
		return cleanupErr
	}
	loginClient, err := client.New(client.Config{BaseURL: dashboardInstance.URL()})
	if err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	if _, err := loginClient.Login(ctx, client.LoginRequest{Username: legacyFMForeignUsername, Password: legacyFMForeignPassword}); err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	pat, err := client.DoREST[patRequest, patResponse](ctx, loginClient, client.RESTRequest[patRequest]{
		Method: http.MethodPost,
		Path:   "/api/v1/api-tokens",
		Body: &patRequest{Name: "agentcompat-fm-foreign", Scopes: []string{
			"nezha:server:read", "nezha:server:write", "nezha:server:delete",
		}},
	})
	if err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	foreign, err := dashboardInstance.AuthenticatedClient(pat.Token)
	if err != nil {
		return nil, func() error { return nil }, errors.Join(err, cleanup())
	}
	return foreign, cleanup, nil
}

func readLegacyFMDownload(ctx context.Context, connection *client.WebSocketConnection, size uint64) ([]byte, int, error) {
	content := make([]byte, 0, size)
	frameCount := 0
	for uint64(len(content)) < size {
		frame, err := readBinaryFrame(ctx, connection)
		if err != nil {
			return nil, frameCount, err
		}
		frameCount++
		if message, ok := parseLegacyFMError(frame); ok {
			return nil, frameCount, message
		}
		remaining := size - uint64(len(content))
		if uint64(len(frame)) > remaining {
			return nil, frameCount, errLegacyFMUnexpected
		}
		content = append(content, frame...)
	}
	return content, frameCount, nil
}

func waitForLegacyFMSessionCleanup(ctx context.Context, owner *client.Client, session string) (int, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := owner.DialWebSocket(ctx, "/api/v1/ws/file/"+session)
		if isLegacyFMSessionRejected(err) {
			return 0, nil
		}
		if connection != nil {
			_ = connection.Close()
		}
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		case <-ticker.C:
		}
	}
}

func cleanupLegacyFMFixtures(ctx context.Context, filesystem mcpFilesystemClient) error {
	deleted, err := filesystem.delete(ctx, "legacy", true)
	if err != nil {
		return err
	}
	if deleted.StructuredContent.DeletedCount != 5 {
		return fmt.Errorf("FM cleanup deleted %d entries, want 5", deleted.StructuredContent.DeletedCount)
	}
	remaining, err := filesystem.list(ctx, ".", true)
	if err != nil {
		return err
	}
	if remaining.StructuredContent.Total != 0 || len(remaining.StructuredContent.Entries) != 0 {
		return errors.New("FM fixture residue remains after cleanup")
	}
	return nil
}

func isLegacyFMSessionRejected(err error) bool {
	if isForbidden(err) {
		return true
	}
	var handshakeErr *client.WebSocketHandshakeError
	return errors.As(err, &handshakeErr) && strings.Contains(handshakeErr.Message, "permission denied")
}

func readBinaryFrame(ctx context.Context, connection *client.WebSocketConnection) ([]byte, error) {
	frame, err := connection.ReadFrame(ctx)
	if err != nil {
		return nil, err
	}
	if frame.Type != client.FrameBinary {
		return nil, errLegacyFMUnexpected
	}
	return frame.Payload, nil
}

func finishLegacyFM(assertions *AssertionSet, runErr error) (Result, error) {
	for _, assertion := range assertions.assertions {
		if !assertion.Passed && runErr == nil {
			runErr = fmt.Errorf("%s: %s", assertion.Name, assertion.Details)
		}
	}
	result := Result{Name: "legacy-fm", Passed: runErr == nil, Assertions: assertions.Results(), CleanupOK: true}
	if runErr != nil {
		result.Error = errorText(runErr)
	}
	return result, runErr
}
