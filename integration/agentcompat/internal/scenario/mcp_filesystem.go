//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/agent"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

const mcpFilesystemScenarioName = "mcp-filesystem"

type MCPFilesystemInput struct {
	Paths contract.Paths
}

type MCPFilesystem struct{}

type mcpFilesystemClient struct {
	client   *client.Client
	serverID uint64
	root     fixture.AgentRoot
}

type mcpFilesystemWrite struct {
	relative      string
	content       string
	encoding      string
	mode          string
	ifMatchSHA256 string
	createDirs    bool
}

func newMCPFilesystemClient(mcpClient *client.Client, serverID uint64, root fixture.AgentRoot) mcpFilesystemClient {
	return mcpFilesystemClient{client: mcpClient, serverID: serverID, root: root}
}

func (filesystem mcpFilesystemClient) list(ctx context.Context, relative string, showHidden bool) (client.ToolCallResult[client.FsListResult], error) {
	path, err := filesystem.root.Path(relative)
	if err != nil {
		return client.ToolCallResult[client.FsListResult]{}, err
	}
	arguments := client.FsListArguments{ServerID: filesystem.serverID, Path: path.String(), ShowHidden: showHidden}
	return client.CallTool[client.FsListArguments, client.FsListResult](ctx, filesystem.client, client.ToolCall[client.FsListArguments]{Name: "fs.list", Arguments: arguments})
}

func (filesystem mcpFilesystemClient) read(ctx context.Context, relative string, offset, length int64, encoding string) (client.ToolCallResult[client.FsReadResult], error) {
	path, err := filesystem.root.Path(relative)
	if err != nil {
		return client.ToolCallResult[client.FsReadResult]{}, err
	}
	arguments := client.FsReadArguments{ServerID: filesystem.serverID, Path: path.String(), Offset: offset, Length: length, Encoding: encoding}
	return client.CallTool[client.FsReadArguments, client.FsReadResult](ctx, filesystem.client, client.ToolCall[client.FsReadArguments]{Name: "fs.read", Arguments: arguments})
}

func (filesystem mcpFilesystemClient) write(ctx context.Context, write mcpFilesystemWrite) (client.ToolCallResult[client.FsWriteResult], error) {
	path, err := filesystem.root.Path(write.relative)
	if err != nil {
		return client.ToolCallResult[client.FsWriteResult]{}, err
	}
	arguments := client.FsWriteArguments{ServerID: filesystem.serverID, Path: path.String(), Content: write.content, Encoding: write.encoding, Mode: write.mode, IfMatchSHA256: write.ifMatchSHA256, CreateDirs: write.createDirs}
	return client.CallTool[client.FsWriteArguments, client.FsWriteResult](ctx, filesystem.client, client.ToolCall[client.FsWriteArguments]{Name: "fs.write", Arguments: arguments})
}

func (filesystem mcpFilesystemClient) delete(ctx context.Context, relative string, recursive bool) (client.ToolCallResult[client.FsDeleteResult], error) {
	path, err := filesystem.root.DestructivePath(relative)
	if err != nil {
		return client.ToolCallResult[client.FsDeleteResult]{}, err
	}
	arguments := client.FsDeleteArguments{ServerID: filesystem.serverID, Path: path.String(), Recursive: recursive}
	return client.CallTool[client.FsDeleteArguments, client.FsDeleteResult](ctx, filesystem.client, client.ToolCall[client.FsDeleteArguments]{Name: "fs.delete", Arguments: arguments})
}

func (MCPFilesystem) Run(ctx context.Context, input MCPFilesystemInput) (result Result, runErr error) {
	assertions := NewAssertionSet()
	fixtureParent, err := os.MkdirTemp("", "agentcompat-mcp-filesystem-")
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	root, err := fixture.NewAgentRoot(fixtureParent, "agent-filesystem")
	if err != nil {
		_ = os.RemoveAll(fixtureParent)
		return mcpFilesystemFinish(assertions, err)
	}
	dashboardInstance, err := dashboard.Start(ctx, dashboard.StartConfig{SourceDir: input.Paths.NezhaSource().String(), ReceiptGate: true})
	if err != nil {
		_ = os.RemoveAll(fixtureParent)
		return mcpFilesystemFinish(assertions, err)
	}
	defer func() {
		cleanupErr := errors.Join(dashboardInstance.Stop(context.Background()), os.RemoveAll(fixtureParent))
		result.CleanupOK = cleanupErr == nil && dashboardInstance.CleanupReceipt().Passed
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
			result.Passed = false
			result.Error = errorText(cleanupErr)
		}
	}()
	agentInstance, err := agent.Start(ctx, agent.AgentStartConfig{SourceDir: input.Paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(), Secret: dashboardInstance.AgentSecret(), UUID: "00000000-0000-0000-0000-000000000212"})
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	defer func() {
		cleanupErr := agentInstance.Stop(context.Background())
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
			result.Passed = false
			result.Error = errorText(cleanupErr)
		}
	}()
	if err := dashboardInstance.WaitForReceiptAccepted(ctx); err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	if err := dashboardInstance.ReleaseReceipt(ctx); err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	readiness, err := agentInstance.WaitReady(ctx, dashboardInstance)
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	mcpClient, err := createScopedClient(ctx, dashboardInstance, []string{"nezha:*"})
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	initialize, err := mcpClient.Initialize(ctx)
	assertions.Record("MCP initialize exact protocol and server", err == nil && initialize.ProtocolVersion == "2024-11-05" && initialize.ServerInfo.Name == "nezha-mcp" && initialize.ServerInfo.Version != "", errorText(err))
	tools, err := mcpClient.ListTools(ctx)
	toolNames := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	wantTools := []string{"fs.delete", "fs.list", "fs.read", "fs.write", "meta.whoami", "server.get", "server.list"}
	assertions.Record("tools.list exposes filesystem identity and inventory tools", err == nil && containsAll(toolNames, wantTools), errorText(err))
	whoami, err := client.CallTool[struct{}, client.WhoAmIResult](ctx, mcpClient, client.ToolCall[struct{}]{Name: "meta.whoami", Arguments: struct{}{}})
	identity := whoami.StructuredContent
	assertions.Record("meta.whoami exact administrator PAT identity", err == nil && identity.UserID != 0 && identity.IsAdmin && identity.TokenID != 0 && identity.TokenName == "agentcompat-scope-check" && slices.Equal(identity.Scopes, []string{"nezha:*"}) && len(identity.ServerIDs) == 0, errorText(err))
	servers, err := client.CallTool[client.ServerListArguments, client.ServerListResult](ctx, mcpClient, client.ToolCall[client.ServerListArguments]{Name: "server.list", Arguments: client.ServerListArguments{OnlineOnly: true}})
	serverID := uint64(0)
	for _, server := range servers.StructuredContent.Servers {
		if server.UUID == agentInstance.UUID() && server.Online {
			serverID = server.ID
		}
	}
	assertions.Record("server.list exact online UUID and count", err == nil && readiness.UUID == agentInstance.UUID() && serverID != 0 && servers.StructuredContent.Count == len(servers.StructuredContent.Servers), errorText(err))
	server, err := client.CallTool[client.ServerGetArguments, client.ServerGetResult](ctx, mcpClient, client.ToolCall[client.ServerGetArguments]{Name: "server.get", Arguments: client.ServerGetArguments{ServerID: serverID}})
	assertions.Record("server.get exact typed identity Host and State", err == nil && server.StructuredContent.ID == serverID && server.StructuredContent.UUID == agentInstance.UUID() && string(server.StructuredContent.Host) != "null" && string(server.StructuredContent.State) != "null", errorText(err))
	if err != nil || serverID == 0 {
		return mcpFilesystemFinish(assertions, errors.New("filesystem scenario inventory setup failed"))
	}
	filesystem := newMCPFilesystemClient(mcpClient, serverID, root)
	if err := verifyMCPFilesystemPathGuards(ctx, assertions, filesystem, fixtureParent); err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	const agentFixtureIdentity = 65534
	permissionCredential := &syscall.Credential{Uid: agentFixtureIdentity, Gid: agentFixtureIdentity}
	permissionAgent, err := agent.Start(ctx, agent.AgentStartConfig{SourceDir: input.Paths.AgentSource().String(), Endpoint: dashboardInstance.Endpoint(), Secret: dashboardInstance.AgentSecret(), UUID: "00000000-0000-0000-0000-000000000213", Credential: permissionCredential})
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	defer func() {
		cleanupErr := permissionAgent.Stop(context.Background())
		if cleanupErr != nil && runErr == nil {
			runErr = cleanupErr
			result.Passed = false
			result.Error = errorText(cleanupErr)
		}
	}()
	if _, err := permissionAgent.WaitReady(ctx, dashboardInstance); err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	permissionClient, err := createScopedClient(ctx, dashboardInstance, []string{"nezha:*"})
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	permissionServers, err := client.CallTool[client.ServerListArguments, client.ServerListResult](ctx, permissionClient, client.ToolCall[client.ServerListArguments]{Name: "server.list", Arguments: client.ServerListArguments{OnlineOnly: true}})
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	permissionServerID := uint64(0)
	for _, server := range permissionServers.StructuredContent.Servers {
		if server.UUID == permissionAgent.UUID() && server.Online {
			permissionServerID = server.ID
		}
	}
	if permissionServerID == 0 {
		return mcpFilesystemFinish(assertions, errors.New("permission Agent is absent from server.list"))
	}
	permissionContract, err := observePermissionAgent(permissionAgent, permissionCredential, newMCPFilesystemClient(permissionClient, permissionServerID, root))
	if err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	if err := runMCPFilesystemOperations(ctx, assertions, filesystem, permissionContract, dashboardInstance); err != nil {
		return mcpFilesystemFinish(assertions, err)
	}
	return mcpFilesystemFinish(assertions, nil)
}

func containsAll(values, required []string) bool {
	for _, value := range required {
		if !slices.Contains(values, value) {
			return false
		}
	}
	return true
}

func mcpFilesystemFinish(assertions *AssertionSet, runErr error) (Result, error) {
	failedAssertion := false
	for _, assertion := range assertions.assertions {
		if !assertion.Passed {
			failedAssertion = true
			if runErr == nil {
				runErr = fmt.Errorf("%s: %s", assertion.Name, assertion.Details)
			}
		}
	}
	if runErr != nil && !failedAssertion {
		assertions.Record("scenario execution completed", false, errorText(runErr))
	}
	result := Result{Name: mcpFilesystemScenarioName, Passed: runErr == nil, Assertions: assertions.Results()}
	if runErr != nil {
		result.Error = evidence.Redact(runErr.Error())
	}
	return result, runErr
}
