//go:build linux

package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

var transferSentinelContent = []byte("outside-transfer-root-sentinel")

type transferSentinels struct {
	root  *os.Root
	names []string
}

func newTransferSentinels(root fixture.AgentRoot, workspaceRoot string) (result transferSentinels, err error) {
	workspace, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return transferSentinels{}, fmt.Errorf("open transfer sentinel root: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, workspace.Close())
		}
	}()
	result = transferSentinels{root: workspace, names: []string{"outside-transfer-sentinel", "outside-transfer-directory/target-sentinel"}}
	if err := workspace.Mkdir("outside-transfer-directory", 0o700); err != nil {
		return transferSentinels{}, fmt.Errorf("create transfer sentinel directory: %w", err)
	}
	for _, name := range result.names {
		if err := workspace.WriteFile(name, transferSentinelContent, 0o600); err != nil {
			return transferSentinels{}, fmt.Errorf("write transfer sentinel: %w", err)
		}
	}
	symlinkPath := filepath.Join(root.Absolute(), "linked")
	if err := os.Symlink(filepath.Join(workspaceRoot, "outside-transfer-directory"), symlinkPath); err != nil {
		return transferSentinels{}, fmt.Errorf("create transfer sentinel symlink: %w", err)
	}
	if _, err := root.Path("../outside-transfer-sentinel"); err == nil {
		return transferSentinels{}, errors.New("transfer AgentPath accepted parent escape")
	}
	if _, err := root.Path("linked/target-sentinel"); err == nil {
		return transferSentinels{}, errors.New("transfer AgentPath accepted symlink parent")
	}
	return result, nil
}

func (sentinels transferSentinels) unchanged() (bool, error) {
	for _, name := range sentinels.names {
		content, err := sentinels.root.ReadFile(name)
		if err != nil {
			return false, fmt.Errorf("read transfer sentinel: %w", err)
		}
		if !bytes.Equal(content, transferSentinelContent) {
			return false, nil
		}
	}
	return true, nil
}

func (sentinels transferSentinels) close() error { return sentinels.root.Close() }
