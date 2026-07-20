//go:build linux && agentcompat

package scenario

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

func TestMCPFilesystemScenario_RealFlow(t *testing.T) {
	nezhaSource := os.Getenv("AGENTCOMPAT_NEZHA_SOURCE")
	agentSource := os.Getenv("AGENTCOMPAT_AGENT_SOURCE")
	if nezhaSource == "" || agentSource == "" {
		t.Skip("set AGENTCOMPAT_NEZHA_SOURCE and AGENTCOMPAT_AGENT_SOURCE")
	}
	paths, err := contract.NewPaths(nezhaSource, agentSource, t.TempDir())
	require.NoError(t, err)

	result, err := (MCPFilesystem{}).Run(t.Context(), MCPFilesystemInput{Paths: paths})

	for _, assertion := range result.Assertions {
		t.Logf("assertion=%q passed=%t details=%q", assertion.Name, assertion.Passed, assertion.Details)
	}
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.True(t, result.CleanupOK)
	requireScenarioAssertions(t, result,
		"fixture path rejections dispatch zero MCP HTTP requests",
		"fixture path rejections leave outside and symlink targets unchanged",
		"fs.write Agent filesystem permission denial is typed",
		"fs.write Agent oversize contract is typed",
	)
	requireScenarioAssertionDetails(t, result, map[string]string{
		"fixture path rejections dispatch zero MCP HTTP requests": "path_rejections_dispatched: 0",
		"fs.write Agent filesystem permission denial is typed":    "uid=65534 gid=65534 agent_handler=true",
		"fs.write Agent oversize contract is typed":               "agent_handler=true production_max_write_check=true",
	})
	requireScenarioAssertionDetails(t, result, map[string]string{
		"fs.write Agent filesystem permission denial is typed": "process_contract=true agent_rpc_response=true",
		"fs.write Agent oversize contract is typed":            "agent_rpc_response=true",
	})
}

func requireScenarioAssertions(t *testing.T, result Result, names ...string) {
	t.Helper()
	assertions := make(map[string]bool, len(result.Assertions))
	for _, assertion := range result.Assertions {
		assertions[assertion.Name] = assertion.Passed
	}
	for _, name := range names {
		require.Truef(t, assertions[name], "required passing assertion %q is absent", name)
	}
}

func requireScenarioAssertionDetails(t *testing.T, result Result, expected map[string]string) {
	t.Helper()
	details := make(map[string]string, len(result.Assertions))
	for _, assertion := range result.Assertions {
		details[assertion.Name] = assertion.Details
	}
	for name, exact := range expected {
		require.Containsf(t, details[name], exact, "assertion %q lacks required computed evidence", name)
	}
}

func TestMCPFilesystemClient_RejectsUnsafeFixturePathsBeforeDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reason    fixture.PathRejectionReason
		configure func(t *testing.T, root fixture.AgentRoot) (string, string)
		invoke    func(context.Context, mcpFilesystemClient, string) error
	}{
		{
			name:   "absolute",
			reason: fixture.PathRejectionAbsolute,
			configure: func(t *testing.T, _ fixture.AgentRoot) (string, string) {
				return filepath.Join(t.TempDir(), "outside"), ""
			},
			invoke: func(ctx context.Context, filesystem mcpFilesystemClient, candidate string) error {
				_, err := filesystem.read(ctx, candidate, 0, 1, "utf8")
				return err
			},
		},
		{
			name:   "parent",
			reason: fixture.PathRejectionParent,
			configure: func(*testing.T, fixture.AgentRoot) (string, string) {
				return "../outside", ""
			},
			invoke: func(ctx context.Context, filesystem mcpFilesystemClient, candidate string) error {
				_, err := filesystem.write(ctx, mcpFilesystemWrite{relative: candidate, content: "changed", encoding: "utf8", mode: "0600", createDirs: true})
				return err
			},
		},
		{
			name:   "volume",
			reason: fixture.PathRejectionVolume,
			configure: func(*testing.T, fixture.AgentRoot) (string, string) {
				return `C:\outside.txt`, ""
			},
			invoke: func(ctx context.Context, filesystem mcpFilesystemClient, candidate string) error {
				_, err := filesystem.list(ctx, candidate, false)
				return err
			},
		},
		{
			name:   "separator",
			reason: fixture.PathRejectionSeparator,
			configure: func(*testing.T, fixture.AgentRoot) (string, string) {
				return `inside\outside.txt`, ""
			},
			invoke: func(ctx context.Context, filesystem mcpFilesystemClient, candidate string) error {
				_, err := filesystem.read(ctx, candidate, 0, 1, "base64")
				return err
			},
		},
		{
			name:   "destructive root",
			reason: fixture.PathRejectionDestructiveRoot,
			configure: func(*testing.T, fixture.AgentRoot) (string, string) {
				return ".", ""
			},
			invoke: func(ctx context.Context, filesystem mcpFilesystemClient, candidate string) error {
				_, err := filesystem.delete(ctx, candidate, true)
				return err
			},
		},
		{
			name:   "symlink parent",
			reason: fixture.PathRejectionSymlinkParent,
			configure: func(t *testing.T, root fixture.AgentRoot) (string, string) {
				outside := t.TempDir()
				target := filepath.Join(outside, "file.txt")
				require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o600))
				require.NoError(t, os.Symlink(outside, filepath.Join(root.Absolute(), "linked")))
				return "linked/file.txt", target
			},
			invoke: func(ctx context.Context, filesystem mcpFilesystemClient, candidate string) error {
				_, err := filesystem.write(ctx, mcpFilesystemWrite{relative: candidate, content: "changed", encoding: "utf8", mode: "0600", createDirs: true})
				return err
			},
		},
		{
			name:   "symlink final",
			reason: fixture.PathRejectionSymlinkFinal,
			configure: func(t *testing.T, root fixture.AgentRoot) (string, string) {
				target := filepath.Join(t.TempDir(), "outside")
				require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0o600))
				require.NoError(t, os.Symlink(target, filepath.Join(root.Absolute(), "linked.txt")))
				return "linked.txt", target
			},
			invoke: func(ctx context.Context, filesystem mcpFilesystemClient, candidate string) error {
				_, err := filesystem.delete(ctx, candidate, false)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}))
			t.Cleanup(server.Close)
			mcpClient, err := client.New(client.Config{BaseURL: server.URL, RequestTimeout: time.Second})
			require.NoError(t, err)
			parent := t.TempDir()
			sentinel := filepath.Join(parent, "outside-sentinel")
			require.NoError(t, os.WriteFile(sentinel, []byte("unchanged"), 0o600))
			root, err := fixture.NewAgentRoot(parent, "mcp-filesystem")
			require.NoError(t, err)
			filesystem := newMCPFilesystemClient(mcpClient, 7, root)
			candidate, symlinkTarget := test.configure(t, root)

			err = test.invoke(t.Context(), filesystem, candidate)

			var pathError *fixture.AgentPathError
			require.ErrorAs(t, err, &pathError)
			require.Equal(t, test.reason, pathError.Reason)
			require.Zero(t, requests.Load())
			content, readErr := os.ReadFile(sentinel)
			require.NoError(t, readErr)
			require.Equal(t, "unchanged", string(content))
			if symlinkTarget != "" {
				content, readErr = os.ReadFile(symlinkTarget)
				require.NoError(t, readErr)
				require.Equal(t, "unchanged", string(content))
			}
		})
	}
}

func TestMCPFilesystemClient_UsesAgentPathForExactToolArguments(t *testing.T) {
	t.Parallel()

	requests := make(chan testFilesystemToolCall, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call, err := decodeTestFilesystemToolCall(request.Body)
		require.NoError(t, err)
		requests <- call
		writer.Header().Set("Content-Type", "application/json")
		_, err = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"size":7,"sha256":"239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5"}}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	mcpClient, err := client.New(client.Config{BaseURL: server.URL, RequestTimeout: time.Second})
	require.NoError(t, err)
	root, err := fixture.NewAgentRoot(t.TempDir(), "mcp-filesystem")
	require.NoError(t, err)
	filesystem := newMCPFilesystemClient(mcpClient, 17, root)

	result, err := filesystem.write(t.Context(), mcpFilesystemWrite{relative: "nested/payload.txt", content: "payload", encoding: "utf8", mode: "0640", createDirs: true})

	require.NoError(t, err)
	require.Equal(t, int64(7), result.StructuredContent.Size)
	require.Equal(t, "239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5", result.StructuredContent.SHA256)
	call := <-requests
	require.Equal(t, "fs.write", call.Name)
	require.Equal(t, uint64(17), call.Arguments.ServerID)
	require.True(t, filepath.IsAbs(call.Arguments.Path))
	require.Equal(t, filepath.Join(root.Absolute(), "nested", "payload.txt"), call.Arguments.Path)
	require.Equal(t, "payload", call.Arguments.Content)
	require.Equal(t, "utf8", call.Arguments.Encoding)
	require.Equal(t, "0640", call.Arguments.Mode)
	require.True(t, call.Arguments.CreateDirs)
}

type testFilesystemToolCall struct {
	Name      string
	Arguments client.FsWriteArguments
}

func decodeTestFilesystemToolCall(body io.Reader) (testFilesystemToolCall, error) {
	var envelope struct {
		Params struct {
			Name      string                  `json:"name"`
			Arguments client.FsWriteArguments `json:"arguments"`
		} `json:"params"`
	}
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		return testFilesystemToolCall{}, err
	}
	return testFilesystemToolCall{Name: envelope.Params.Name, Arguments: envelope.Params.Arguments}, nil
}
