//go:build linux

package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Spec struct {
	Name             string
	Path             string
	Args             []string
	Dir              string
	Env              []string
	ExtraFiles       []*os.File
	Stdout           io.Writer
	Stderr           io.Writer
	MaxLogBytes      int
	TerminateTimeout time.Duration
	KillTimeout      time.Duration
	Readiness        func(Stream, string) bool
	Credential       *syscall.Credential
}

func (spec Spec) validate() error {
	if spec.Name == "" || spec.Path == "" || spec.MaxLogBytes < 1 || spec.TerminateTimeout <= 0 || spec.KillTimeout <= 0 {
		return errors.New("invalid process specification")
	}
	if !filepath.IsAbs(spec.Path) {
		return errors.New("process path must be absolute")
	}
	info, err := os.Stat(spec.Path)
	if err != nil {
		return fmt.Errorf("stat process path: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("process path must be an executable regular file")
	}
	return nil
}
