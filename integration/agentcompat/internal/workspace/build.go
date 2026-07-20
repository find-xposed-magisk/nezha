//go:build linux

package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type BuildSpec struct {
	Name      string
	SourceDir string
	Package   string
	Tags      []string
	Ldflags   []string
	Env       []string
}

func (workspace *Workspace) Build(ctx context.Context, spec BuildSpec) (string, error) {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if err := workspace.requireOpen(); err != nil {
		return "", err
	}
	if spec.SourceDir == "" || spec.Package == "" {
		return "", errors.New("build source and package are required")
	}
	if err := validateLeafName(spec.Name); err != nil {
		return "", err
	}
	binaryPath := filepath.Join(workspace.binDir, spec.Name)
	arguments := []string{"build", "-mod=readonly", "-o", binaryPath}
	if len(spec.Tags) > 0 {
		arguments = append(arguments, "-tags", strings.Join(spec.Tags, ","))
	}
	if len(spec.Ldflags) > 0 {
		arguments = append(arguments, "-ldflags", strings.Join(spec.Ldflags, " "))
	}
	arguments = append(arguments, spec.Package)
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = spec.SourceDir
	command.Env = spec.Env
	if spec.Env == nil {
		command.Env = os.Environ()
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build %s: %w: %s", spec.Name, err, output)
	}
	return binaryPath, nil
}
