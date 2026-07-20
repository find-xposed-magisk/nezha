//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

type PreparedBinaryUsageError struct {
	Operation string
	Reason    string
}

func (err *PreparedBinaryUsageError) Error() string {
	return fmt.Sprintf("prepared agent binary %s: %s", err.Operation, err.Reason)
}

// PreparedBinary owns a build-only workspace. Each successful Start lease keeps
// that workspace alive; Agent workspaces own their own config, logs, and process tracking.
type PreparedBinary struct {
	workspace  *workspace.Workspace
	binaryPath string
	mu         sync.Mutex
	consumers  int
	closed     bool
}

func PrepareBinary(ctx context.Context, sourceDir string) (*PreparedBinary, error) {
	if err := validateSourceDir(sourceDir); err != nil {
		return nil, &PreparedBinaryUsageError{Operation: "prepare", Reason: err.Error()}
	}
	workspaceRoot, err := workspace.New(context.WithoutCancel(ctx))
	if err != nil {
		return nil, fmt.Errorf("create prepared agent workspace: %w", err)
	}
	binaryPath, err := workspaceRoot.Build(ctx, agentBuildSpec(sourceDir))
	if err != nil {
		return nil, closePreparedWorkspace(workspaceRoot, err)
	}
	if err := exposePreparedBinary(workspaceRoot.Root(), binaryPath); err != nil {
		return nil, closePreparedWorkspace(workspaceRoot, err)
	}
	return &PreparedBinary{workspace: workspaceRoot, binaryPath: binaryPath}, nil
}

func closePreparedWorkspace(workspaceRoot *workspace.Workspace, cause error) error {
	return fmt.Errorf("prepare agent binary: %w", closeError(cause, workspaceRoot.Close()))
}

func exposePreparedBinary(root, binaryPath string) error {
	for _, path := range []string{root, filepath.Dir(binaryPath)} {
		if err := os.Chmod(path, 0o755); err != nil {
			return fmt.Errorf("make prepared agent binary executable: %w", err)
		}
	}
	return nil
}

func (prepared *PreparedBinary) BinaryPath() string {
	if prepared == nil {
		return ""
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	return prepared.binaryPath
}

func (prepared *PreparedBinary) WorkspaceRoot() string {
	if prepared == nil {
		return ""
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	return prepared.workspace.Root()
}

func (prepared *PreparedBinary) acquire() (string, func(), error) {
	if prepared == nil {
		return "", nil, &PreparedBinaryUsageError{Operation: "start", Reason: "is nil"}
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.workspace == nil || prepared.binaryPath == "" {
		return "", nil, &PreparedBinaryUsageError{Operation: "start", Reason: "is uninitialized"}
	}
	if prepared.closed {
		return "", nil, &PreparedBinaryUsageError{Operation: "start", Reason: "is closed"}
	}
	prepared.consumers++
	return prepared.binaryPath, prepared.release, nil
}

func (prepared *PreparedBinary) release() {
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	prepared.consumers--
}

func (prepared *PreparedBinary) Close() error {
	if prepared == nil {
		return &PreparedBinaryUsageError{Operation: "close", Reason: "is nil"}
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	if prepared.workspace == nil || prepared.binaryPath == "" {
		return &PreparedBinaryUsageError{Operation: "close", Reason: "is uninitialized"}
	}
	if prepared.closed {
		return nil
	}
	if prepared.consumers != 0 {
		return &PreparedBinaryUsageError{Operation: "close", Reason: "has active consumers"}
	}
	if err := prepared.workspace.Close(); err != nil {
		return fmt.Errorf("close prepared agent workspace: %w", err)
	}
	prepared.closed = true
	return nil
}
