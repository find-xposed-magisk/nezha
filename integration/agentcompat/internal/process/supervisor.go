//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
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

type Supervisor struct {
	ctx         context.Context
	spec        Spec
	cmd         *exec.Cmd
	pid         int
	pgid        int
	ready       chan struct{}
	readyOnce   sync.Once
	exited      chan struct{}
	waitErr     error
	waitMu      sync.Mutex
	cleanupOnce sync.Once
	cleanupDone chan struct{}
	cleanupErr  error
	forced      bool
	stateMu     sync.Mutex
	stdoutLog   *boundedLog
	stderrLog   *boundedLog
}

func NewSupervisor(ctx context.Context, spec Spec) *Supervisor {
	return &Supervisor{ctx: ctx, spec: spec, ready: make(chan struct{}), exited: make(chan struct{}), cleanupDone: make(chan struct{})}
}

func (supervisor *Supervisor) Start() error {
	if supervisor.spec.Name == "" || supervisor.spec.Path == "" || supervisor.spec.MaxLogBytes < 1 || supervisor.spec.TerminateTimeout <= 0 || supervisor.spec.KillTimeout <= 0 {
		return errors.New("invalid process specification")
	}
	command := exec.Command(supervisor.spec.Path, supervisor.spec.Args...)
	command.Dir = supervisor.spec.Dir
	command.Env = supervisor.spec.Env
	if command.Env == nil {
		command.Env = os.Environ()
	}
	command.ExtraFiles = supervisor.spec.ExtraFiles
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL, Credential: supervisor.spec.Credential}
	supervisor.stdoutLog = newBoundedLog(supervisor.spec.Stdout, supervisor.spec.MaxLogBytes, supervisor.lineObserver(Stdout))
	supervisor.stderrLog = newBoundedLog(supervisor.spec.Stderr, supervisor.spec.MaxLogBytes, supervisor.lineObserver(Stderr))
	command.Stdout = supervisor.stdoutLog
	command.Stderr = supervisor.stderrLog
	if err := command.Start(); err != nil {
		supervisor.closeExtraFiles()
		return fmt.Errorf("start %s: %w", supervisor.spec.Name, err)
	}
	supervisor.closeExtraFiles()
	supervisor.cmd = command
	supervisor.pid = command.Process.Pid
	supervisor.pgid = command.Process.Pid
	go supervisor.reap()
	go supervisor.watchContext()
	return nil
}

func (supervisor *Supervisor) lineObserver(stream Stream) func(string) {
	return func(line string) {
		if supervisor.spec.Readiness != nil && supervisor.spec.Readiness(stream, line) {
			supervisor.SignalReady()
		}
	}
}

func (supervisor *Supervisor) closeExtraFiles() {
	for _, file := range supervisor.spec.ExtraFiles {
		if file != nil {
			_ = file.Close()
		}
	}
}

func (supervisor *Supervisor) reap() {
	err := supervisor.cmd.Wait()
	supervisor.stdoutLog.Close()
	supervisor.stderrLog.Close()
	supervisor.waitMu.Lock()
	supervisor.waitErr = err
	supervisor.waitMu.Unlock()
	close(supervisor.exited)
}

func (supervisor *Supervisor) watchContext() {
	select {
	case <-supervisor.ctx.Done():
		_ = supervisor.Stop(context.WithoutCancel(supervisor.ctx))
	case <-supervisor.exited:
	}
}

func (supervisor *Supervisor) SignalReady() {
	supervisor.readyOnce.Do(func() { close(supervisor.ready) })
}

func (supervisor *Supervisor) Ready() <-chan struct{} { return supervisor.ready }

func (supervisor *Supervisor) Exited() <-chan struct{} { return supervisor.exited }

func (supervisor *Supervisor) WaitReady(ctx context.Context) error {
	select {
	case <-supervisor.ready:
		return nil
	default:
	}
	select {
	case <-supervisor.ready:
		return nil
	case <-supervisor.exited:
		select {
		case <-supervisor.ready:
			return nil
		default:
			return errors.New("process exited before readiness")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (supervisor *Supervisor) Wait(ctx context.Context) error {
	select {
	case <-supervisor.exited:
		cleanupErr := supervisor.Stop(ctx)
		supervisor.waitMu.Lock()
		waitErr := supervisor.waitErr
		supervisor.waitMu.Unlock()
		return errors.Join(waitErr, cleanupErr)
	case <-ctx.Done():
		return errors.Join(ctx.Err(), supervisor.Stop(context.WithoutCancel(ctx)))
	}
}

func (supervisor *Supervisor) Stop(ctx context.Context) error {
	supervisor.cleanupOnce.Do(func() { go supervisor.cleanup() })
	select {
	case <-supervisor.cleanupDone:
		return supervisor.cleanupResult()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (supervisor *Supervisor) cleanup() {
	defer close(supervisor.cleanupDone)
	if supervisor.pgid < 1 {
		return
	}
	if !processGroupExists(supervisor.pgid) {
		supervisor.waitForExit()
		return
	}
	if err := syscall.Kill(-supervisor.pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		supervisor.setCleanupError(fmt.Errorf("terminate %s process group: %w", supervisor.spec.Name, err))
		return
	}
	if waitProcessGroup(supervisor.pgid, supervisor.spec.TerminateTimeout) {
		supervisor.waitForExit()
		return
	}
	supervisor.stateMu.Lock()
	supervisor.forced = true
	supervisor.stateMu.Unlock()
	if err := syscall.Kill(-supervisor.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		supervisor.setCleanupError(fmt.Errorf("kill %s process group: %w", supervisor.spec.Name, err))
		return
	}
	if !waitProcessGroup(supervisor.pgid, supervisor.spec.KillTimeout) {
		supervisor.setCleanupError(fmt.Errorf("%s process group %d survived SIGKILL", supervisor.spec.Name, supervisor.pgid))
		return
	}
	supervisor.waitForExit()
}

func (supervisor *Supervisor) waitForExit() {
	timer := time.NewTimer(supervisor.spec.KillTimeout)
	defer timer.Stop()
	select {
	case <-supervisor.exited:
	case <-timer.C:
		supervisor.setCleanupError(fmt.Errorf("%s process was not reaped", supervisor.spec.Name))
	}
}

func (supervisor *Supervisor) setCleanupError(err error) {
	supervisor.stateMu.Lock()
	supervisor.cleanupErr = errors.Join(supervisor.cleanupErr, err)
	supervisor.stateMu.Unlock()
}

func (supervisor *Supervisor) cleanupResult() error {
	supervisor.stateMu.Lock()
	defer supervisor.stateMu.Unlock()
	return supervisor.cleanupErr
}

func processGroupExists(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitProcessGroup(pgid int, timeout time.Duration) bool {
	if !processGroupExists(pgid) {
		return true
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ticker.C:
			if !processGroupExists(pgid) {
				return true
			}
		case <-timer.C:
			return !processGroupExists(pgid)
		}
	}
}

func (supervisor *Supervisor) PID() int { return supervisor.pid }

func (supervisor *Supervisor) ProcessGroupID() int { return supervisor.pgid }

func (supervisor *Supervisor) ForcedCleanup() bool {
	supervisor.stateMu.Lock()
	defer supervisor.stateMu.Unlock()
	return supervisor.forced
}

func (supervisor *Supervisor) CleanupRecord() CleanupRecord {
	supervisor.stateMu.Lock()
	defer supervisor.stateMu.Unlock()
	return CleanupRecord{Name: supervisor.spec.Name, PID: supervisor.pid, Forced: supervisor.forced, Error: errorString(supervisor.cleanupErr)}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
