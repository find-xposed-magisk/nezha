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
	paths []string
}

func newTransferSentinels(root fixture.AgentRoot, workspaceRoot string) (transferSentinels, error) {
	directPath := filepath.Join(workspaceRoot, "outside-transfer-sentinel")
	symlinkDirectory := filepath.Join(workspaceRoot, "outside-transfer-directory")
	symlinkTarget := filepath.Join(symlinkDirectory, "target-sentinel")
	if err := os.Mkdir(symlinkDirectory, 0o700); err != nil {
		return transferSentinels{}, fmt.Errorf("create transfer sentinel directory: %w", err)
	}
	for _, path := range []string{directPath, symlinkTarget} {
		if err := os.WriteFile(path, transferSentinelContent, 0o600); err != nil {
			return transferSentinels{}, fmt.Errorf("write transfer sentinel: %w", err)
		}
	}
	symlinkPath := filepath.Join(root.Absolute(), "linked")
	if err := os.Symlink(symlinkDirectory, symlinkPath); err != nil {
		return transferSentinels{}, fmt.Errorf("create transfer sentinel symlink: %w", err)
	}
	if _, err := root.Path("../outside-transfer-sentinel"); err == nil {
		return transferSentinels{}, errors.New("transfer AgentPath accepted parent escape")
	}
	if _, err := root.Path("linked/target-sentinel"); err == nil {
		return transferSentinels{}, errors.New("transfer AgentPath accepted symlink parent")
	}
	return transferSentinels{paths: []string{directPath, symlinkTarget}}, nil
}

func (sentinels transferSentinels) unchanged() (bool, error) {
	for _, path := range sentinels.paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read transfer sentinel: %w", err)
		}
		if !bytes.Equal(content, transferSentinelContent) {
			return false, nil
		}
	}
	return true, nil
}
