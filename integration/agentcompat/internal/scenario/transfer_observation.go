//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type transferResidueScope struct {
	AgentRoot    string
	DashboardPID int
}

func confirmTransferQuiescence(ctx context.Context, scope transferResidueScope) (time.Duration, error) {
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return 0, errors.New("transfer quiescence requires a context deadline")
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	residue, err := transferResidue(scope)
	if err != nil {
		return 0, err
	}
	if len(residue) > 0 {
		return 0, fmt.Errorf("transfer completion left residue: %s", strings.Join(residue, ", "))
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}
	return remaining, nil
}

func transferResidue(scope transferResidueScope) ([]string, error) {
	var residue []string
	err := filepath.WalkDir(scope.AgentRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".mcp-xfer-") {
			residue = append(residue, path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	fdDirectory := filepath.Join("/proc", strconv.Itoa(scope.DashboardPID), "fd")
	entries, err := os.ReadDir(fdDirectory)
	if err != nil {
		return nil, fmt.Errorf("read dashboard descriptors: %w", err)
	}
	for _, entry := range entries {
		target, readErr := os.Readlink(filepath.Join(fdDirectory, entry.Name()))
		if readErr != nil {
			continue
		}
		if strings.Contains(target, "nz-mcp-xfer-") {
			residue = append(residue, target)
		}
	}
	return residue, nil
}

func countTransferResidue(residue []string) (agentTemp, dashboardSpool int) {
	for _, path := range residue {
		if strings.Contains(filepath.Base(path), ".mcp-xfer-") {
			agentTemp++
		}
		if strings.Contains(path, "nz-mcp-xfer-") {
			dashboardSpool++
		}
	}
	return agentTemp, dashboardSpool
}
