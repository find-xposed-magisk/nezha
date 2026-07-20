package workflowpolicy_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

const nezhaModulePath = "github.com/nezhahq/nezha"

func TestRepositoryRoot_FindsModuleAcrossLineEndings(t *testing.T) {
	tests := []struct {
		name      string
		start     string
		parents   map[string]string
		goModules map[string][]byte
		wantRoot  string
	}{
		{
			name:  "LF module directive",
			start: "/workspace/nezha/integration/agentcompat/internal/workflowpolicy",
			parents: map[string]string{
				"/workspace/nezha/integration/agentcompat/internal/workflowpolicy": "/workspace/nezha/integration/agentcompat/internal",
				"/workspace/nezha/integration/agentcompat/internal":                "/workspace/nezha/integration/agentcompat",
				"/workspace/nezha/integration/agentcompat":                         "/workspace/nezha/integration",
				"/workspace/nezha/integration":                                     "/workspace/nezha",
				"/workspace/nezha":                                                 "/workspace",
			},
			goModules: map[string][]byte{"/workspace/nezha": []byte("// Nezha Dashboard\nmodule " + nezhaModulePath + "\ngo 1.26.3\n")},
			wantRoot:  "/workspace/nezha",
		},
		{
			name:  "CRLF module directive at Windows root",
			start: `D:\work\nezha\integration\agentcompat\internal\workflowpolicy`,
			parents: map[string]string{
				`D:\work\nezha\integration\agentcompat\internal\workflowpolicy`: `D:\work\nezha\integration\agentcompat\internal`,
				`D:\work\nezha\integration\agentcompat\internal`:                `D:\work\nezha\integration\agentcompat`,
				`D:\work\nezha\integration\agentcompat`:                         `D:\work\nezha\integration`,
				`D:\work\nezha\integration`:                                     `D:\work\nezha`,
				`D:\work\nezha`:                                                 `D:\work`,
				`D:\work`:                                                       `D:\`,
				`D:\`:                                                           `D:\`,
			},
			goModules: map[string][]byte{`D:\work\nezha`: []byte("// Nezha Dashboard\r\nmodule " + nezhaModulePath + "\r\ngo 1.26.3\r\n")},
			wantRoot:  `D:\work\nezha`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			readGoModule := func(directory string) ([]byte, error) {
				goModule, exists := test.goModules[directory]
				if !exists {
					return nil, os.ErrNotExist
				}
				return goModule, nil
			}
			parentDirectory := func(directory string) string {
				parent, exists := test.parents[directory]
				require.True(t, exists, "parent of %q must be defined", directory)
				return parent
			}

			// When
			actualRoot, err := findNezhaRepositoryRoot(test.start, readGoModule, parentDirectory)

			// Then
			require.NoError(t, err)
			require.Equal(t, test.wantRoot, actualRoot)
		})
	}
}

func TestRepositoryRoot_ReturnsUsefulErrorAtFilesystemRoot(t *testing.T) {
	// Given
	const windowsRoot = `D:\`
	parentCalls := 0

	// When
	actualRoot, err := findNezhaRepositoryRoot(windowsRoot, func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}, func(directory string) string {
		parentCalls++
		return directory
	})

	// Then
	require.Empty(t, actualRoot)
	require.Error(t, err)
	require.ErrorContains(t, err, "repository root containing module \"github.com/nezhahq/nezha\" was not found")
	require.ErrorContains(t, err, windowsRoot)
	require.Equal(t, 1, parentCalls)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	root, err := findNezhaRepositoryRoot(workingDirectory, func(directory string) ([]byte, error) {
		return os.ReadFile(filepath.Join(directory, "go.mod"))
	}, filepath.Dir)
	require.NoError(t, err)
	return root
}

func findNezhaRepositoryRoot(start string, readGoModule func(string) ([]byte, error), parentDirectory func(string) string) (string, error) {
	current := start
	for {
		goModule, err := readGoModule(current)
		if err == nil {
			modulePath, parseErr := modulePathFromGoMod(goModule)
			if parseErr != nil {
				return "", fmt.Errorf("parse go.mod in %q: %w", current, parseErr)
			}
			if modulePath == nezhaModulePath {
				return current, nil
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read go.mod in %q: %w", current, err)
		}
		parent := parentDirectory(current)
		if current == parent {
			return "", fmt.Errorf("repository root containing module %q was not found from %q", nezhaModulePath, start)
		}
		current = parent
	}
}

func modulePathFromGoMod(goModule []byte) (string, error) {
	moduleFile, err := modfile.Parse("go.mod", goModule, nil)
	if err != nil {
		return "", err
	}
	if moduleFile.Module == nil {
		return "", fmt.Errorf("module directive is missing")
	}
	return moduleFile.Module.Mod.Path, nil
}
