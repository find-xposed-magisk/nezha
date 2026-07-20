package testpaths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNezhaSource_UsesModuleRootFromNonRepositoryCWD(t *testing.T) {
	t.Setenv("NEZHA_SOURCE", "")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o600))
	nonRepository := t.TempDir()
	original, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(nonRepository))
	t.Cleanup(func() { require.NoError(t, os.Chdir(original)) })

	resolved, err := NezhaSource(root)

	require.NoError(t, err)
	require.Equal(t, root, resolved)
}

func TestSourceResolversHonorExplicitEnvironmentPaths(t *testing.T) {
	// Given
	nezha := t.TempDir()
	agent := t.TempDir()
	t.Setenv("NEZHA_SOURCE", nezha)
	t.Setenv("AGENT_SOURCE", agent)

	// When
	resolvedNezha, nezhaErr := NezhaSource(t.TempDir())
	resolvedAgent, agentErr := AgentSource(nezha)

	// Then
	require.NoError(t, nezhaErr)
	require.NoError(t, agentErr)
	require.Equal(t, nezha, resolvedNezha)
	require.Equal(t, agent, resolvedAgent)
}

func TestAgentSource_ResolvesAdjacentCheckoutThroughSymlink(t *testing.T) {
	t.Setenv("AGENT_SOURCE", "")
	parent := t.TempDir()
	nezha := filepath.Join(parent, "nezha")
	agent := filepath.Join(parent, "agent")
	require.NoError(t, os.MkdirAll(nezha, 0o700))
	require.NoError(t, os.MkdirAll(agent, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(nezha, "go.mod"), []byte("module nezha.test\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agent, "go.mod"), []byte("module agent.test\n"), 0o600))
	linkParent := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, os.Symlink(parent, linkParent))

	resolved, err := AgentSource(filepath.Join(linkParent, "nezha"))

	require.NoError(t, err)
	require.Equal(t, filepath.Join(linkParent, "agent"), resolved)
}
