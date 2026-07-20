//go:build linux

package process

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessHasOpenPathTogglesWithDescriptorLifecycle(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "dashboard.sqlite-journal")
	require.NoError(t, os.WriteFile(path, []byte("journal"), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)

	// When
	held, err := ProcessHasOpenPath(os.Getpid(), path)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	released, err := ProcessHasOpenPath(os.Getpid(), path)

	// Then
	require.NoError(t, err)
	require.True(t, held)
	require.False(t, released)
}
