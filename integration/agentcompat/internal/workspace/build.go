//go:build linux

package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	goExecutable, err := resolveGoExecutable()
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, goExecutable, arguments...) // #nosec G204 -- Resolved absolute regular Go toolchain executable and fixed argv; no shell is invoked.
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

func resolveGoExecutable() (string, error) {
	candidates := []string{filepath.Join(runtime.GOROOT(), "bin", "go")}
	if path, err := exec.LookPath("go"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return resolved, nil
		}
	}
	return "", errors.New("Go toolchain executable is unavailable")
}
