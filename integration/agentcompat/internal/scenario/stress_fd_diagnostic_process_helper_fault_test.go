//go:build linux && agentcompat

package scenario

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFDDiagnosticShell_NextLineReturnsDeadlineWhenOutputStalls(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	child := startFDDiagnosticShell(t, t.Context(), "")
	defer func() { require.NoError(t, child.close()) }()

	// When
	_, err := child.nextLine(ctx)

	// Then
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestFDDiagnosticShell_CloseReapsChildWhenExitWriteFails(t *testing.T) {
	// Given
	child := startFDDiagnosticShell(t, t.Context(), "")
	require.NoError(t, child.input.Close())

	// When
	err := child.close()

	// Then
	require.Error(t, err)
	require.NotNil(t, child.command.ProcessState)
}
