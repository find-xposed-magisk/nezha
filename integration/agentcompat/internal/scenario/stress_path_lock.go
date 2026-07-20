//go:build linux && agentcompat

package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrStressPathLockProof = errors.New("stress path-lock proof is invalid")

type stressPathLockProof struct {
	Stripes int
}

func (proof stressPathLockProof) Validate() error {
	if proof.Stripes != 1024 {
		return fmt.Errorf("stripes=%d: %w", proof.Stripes, ErrStressPathLockProof)
	}
	return nil
}

func proveStressPathLockStripes(ctx context.Context, sourceDir string) (stressPathLockProof, error) {
	if sourceDir == "" {
		return stressPathLockProof{}, ErrStressPathLockProof
	}
	root, err := os.MkdirTemp("", "nezha-stress-path-lock-")
	if err != nil {
		return stressPathLockProof{}, err
	}
	defer os.RemoveAll(root)
	probe := filepath.Join(root, "agentcompat_path_lock_proof_test.go")
	content := "package main\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\nconst agentCompatPathLockStripes = fsPathLockStripes\n\nvar _ [agentCompatPathLockStripes - 1024]struct{}\nvar _ [1024 - agentCompatPathLockStripes]struct{}\n\nfunc TestAgentCompatPathLockProof(t *testing.T) {\n\tfmt.Printf(\"AGENTCOMPAT_PATH_LOCK_STRIPES=%d\\n\", agentCompatPathLockStripes)\n}\n"
	if err := os.WriteFile(probe, []byte(content), 0o600); err != nil {
		return stressPathLockProof{}, err
	}
	overlayPath := filepath.Join(root, "overlay.json")
	original := filepath.Join(sourceDir, "cmd", "agent", "mcp_fs_path_lock_bounded_test.go")
	overlayData, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{original: probe}})
	if err != nil {
		return stressPathLockProof{}, err
	}
	if err := os.WriteFile(overlayPath, overlayData, 0o600); err != nil {
		return stressPathLockProof{}, err
	}
	command := exec.CommandContext(ctx, "go", "test", "-overlay", overlayPath, "./cmd/agent", "-run", "^TestAgentCompatPathLockProof$", "-count=1", "-v")
	command.Dir = sourceDir
	command.Env = append(os.Environ(), "GOFLAGS=")
	output, err := command.CombinedOutput()
	if err != nil {
		return stressPathLockProof{}, fmt.Errorf("compile path-lock proof: %w: %s", err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "AGENTCOMPAT_PATH_LOCK_STRIPES=") {
			continue
		}
		value, parseErr := strconv.Atoi(strings.TrimPrefix(line, "AGENTCOMPAT_PATH_LOCK_STRIPES="))
		if parseErr == nil {
			proof := stressPathLockProof{Stripes: value}
			return proof, proof.Validate()
		}
	}
	return stressPathLockProof{}, fmt.Errorf("path-lock proof output missing: %w", ErrStressPathLockProof)
}
