//go:build linux

package agent

import (
	"bytes"
	"os"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestAgent_PrepareConfigWritesSkipConnectionCount(t *testing.T) {
	testCases := []struct {
		name                 string
		config               AgentStartConfig
		expectedConfigLine   []byte
		unexpectedConfigLine []byte
	}{
		{
			name:                 "writes enabled value when requested",
			config:               AgentStartConfig{SkipConnectionCount: true},
			expectedConfigLine:   []byte("skip_connection_count: true\n"),
			unexpectedConfigLine: []byte("skip_connection_count: false\n"),
		},
		{
			name:                 "writes disabled value by default",
			expectedConfigLine:   []byte("skip_connection_count: false\n"),
			unexpectedConfigLine: []byte("skip_connection_count: true\n"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// Given
			workspaceRoot, err := workspace.New(t.Context())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, workspaceRoot.Close()) })
			agent := &Agent{workspace: workspaceRoot}

			// When
			err = agent.prepareConfig(testCase.config)

			// Then
			require.NoError(t, err)
			configBytes, err := os.ReadFile(agent.ConfigPath())
			require.NoError(t, err)
			require.True(t, bytes.Contains(configBytes, testCase.expectedConfigLine))
			require.False(t, bytes.Contains(configBytes, testCase.unexpectedConfigLine))
		})
	}
}
