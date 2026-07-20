//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/workspace"
)

const (
	agentSecret      = "0123456789abcdef0123456789abcdef"
	agentMaxLogBytes = 1 << 20
	agentStopTimeout = 5 * time.Second
	agentKillTimeout = 5 * time.Second
)

type AgentStartConfig struct {
	SourceDir         string
	PreparedBinary    *PreparedBinary
	Endpoint          string
	Secret            string
	UUID              string
	TLS               bool
	Debug             bool
	CAFilePath        string
	FMObserverRunID   string
	Credential        *syscall.Credential
	newSupervisor     func(context.Context, processharness.Spec) *processharness.Supervisor
	trackPID          func(int) error
	trackProcessGroup func(int) error
}

type AgentStartError struct {
	cause error
	agent *Agent
}

func (err *AgentStartError) Error() string { return err.cause.Error() }
func (err *AgentStartError) Unwrap() error { return err.cause }
func (err *AgentStartError) Finalize(ctx context.Context) error {
	return err.agent.Stop(ctx)
}

type Agent struct {
	workspace         *workspace.Workspace
	supervisor        *processharness.Supervisor
	clients           dashboard.Clients
	configPath        string
	logPath           string
	binaryPath        string
	caFilePath        string
	environment       []string
	secret            string
	uuid              string
	releaseBinary     func()
	releasePending    bool
	cleanupOnce       sync.Once
	cleanupDone       chan struct{}
	cleanupAttemptMu  sync.Mutex
	cleanupMu         sync.Mutex
	cleanupErr        error
	readinessMu       sync.Mutex
	lastStateReport   time.Time
	fmObserver        *FMProducerObserver
	fmObserverPath    string
	startConfig       AgentStartConfig
	processMu         sync.Mutex
	currentProcess    *processGeneration
	processes         []*processGeneration
	generation        uint64
	closed            bool
	trackPID          func(int) error
	trackProcessGroup func(int) error
}

func Start(ctx context.Context, config AgentStartConfig) (*Agent, error) {
	if config.PreparedBinary == nil {
		if err := validateSourceDir(config.SourceDir); err != nil {
			return nil, err
		}
	}
	if config.Endpoint == "" || config.UUID == "" {
		return nil, errors.New("agent endpoint and UUID are required")
	}
	if config.Secret == "" {
		config.Secret = agentSecret
	}
	workspaceRoot, err := workspace.New(context.WithoutCancel(ctx))
	if err != nil {
		return nil, fmt.Errorf("create agent workspace: %w", err)
	}
	trackPID := workspaceRoot.TrackPID
	if config.trackPID != nil {
		trackPID = config.trackPID
	}
	trackProcessGroup := workspaceRoot.TrackProcessGroup
	if config.trackProcessGroup != nil {
		trackProcessGroup = config.trackProcessGroup
	}
	agent := &Agent{workspace: workspaceRoot, secret: config.Secret, uuid: config.UUID, cleanupDone: make(chan struct{}), startConfig: config, trackPID: trackPID, trackProcessGroup: trackProcessGroup}
	if err := agent.prepareFixture(ctx, config); err != nil {
		return nil, cleanupFailedStart(ctx, agent, err)
	}
	if _, err := agent.StartProcess(ctx); err != nil {
		return nil, cleanupFailedStart(ctx, agent, err)
	}
	go agent.cleanupOnCancellation(ctx)
	return agent, nil
}

func validateSourceDir(sourceDir string) error {
	if sourceDir == "" || !filepath.IsAbs(sourceDir) {
		return errors.New("agent source directory must be absolute")
	}
	return nil
}

func cleanupFailedStart(ctx context.Context, agent *Agent, cause error) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	startError := errors.Join(cause, agent.Stop(cleanupContext))
	if agent.finalizationPending() {
		// A failed rollback can leave the prepared binary leased until its process group exits.
		return &AgentStartError{cause: startError, agent: agent}
	}
	return startError
}

func filteredEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "NZ_") || strings.HasPrefix(value, "SSL_CERT_FILE=") || strings.HasPrefix(value, "AGENTCOMPAT_FM_OBSERVER_") {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (agent *Agent) Stop(ctx context.Context) error {
	agent.cleanupOnce.Do(func() { go agent.cleanup(context.WithoutCancel(ctx)) })
	select {
	case <-agent.cleanupDone:
		agent.retryFinalization(ctx)
		return agent.cleanupResult()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (agent *Agent) cleanupOnCancellation(ctx context.Context) {
	select {
	case <-ctx.Done():
		agent.cleanupOnce.Do(func() { go agent.cleanup(context.WithoutCancel(ctx)) })
	case <-agent.cleanupDone:
	}
}

func (agent *Agent) cleanup(ctx context.Context) {
	defer close(agent.cleanupDone)
	agent.finishCleanup(ctx, true)
}

func (agent *Agent) finishCleanup(ctx context.Context, closeObserver bool) {
	agent.cleanupAttemptMu.Lock()
	defer agent.cleanupAttemptMu.Unlock()
	cleanupError := agent.closeProcesses(ctx)
	if closeObserver && agent.fmObserver != nil {
		cleanupError = errors.Join(cleanupError, agent.fmObserver.Close(), removeFMObserverSocket(agent.fmObserverPath))
	}
	if err := agent.workspace.Close(); err != nil {
		cleanupError = errors.Join(cleanupError, err)
	}
	// The prepared workspace must outlive every consumer process group, even when process cleanup reports an error.
	if agent.releasePending && agent.processesQuiescent() {
		agent.releaseBinary()
		agent.releasePending = false
	}
	agent.cleanupMu.Lock()
	if agent.cleanupErr == nil {
		agent.cleanupErr = cleanupError
	}
	agent.cleanupMu.Unlock()
}

func (agent *Agent) retryFinalization(ctx context.Context) {
	if agent.finalizationPending() {
		agent.finishCleanup(context.WithoutCancel(ctx), false)
	}
}

func (agent *Agent) finalizationPending() bool {
	agent.cleanupAttemptMu.Lock()
	defer agent.cleanupAttemptMu.Unlock()
	return agent.releasePending
}

func (agent *Agent) cleanupResult() error {
	agent.cleanupMu.Lock()
	defer agent.cleanupMu.Unlock()
	return agent.cleanupErr
}

func closeError(first, second error) error { return errors.Join(first, second) }
func (agent *Agent) UUID() string          { return agent.uuid }
func (agent *Agent) PID() int {
	agent.processMu.Lock()
	defer agent.processMu.Unlock()
	if agent.currentProcess == nil {
		return 0
	}
	return agent.currentProcess.identity.PID
}
func (agent *Agent) CleanupReceipt() processharness.CleanupReceipt {
	agent.processMu.Lock()
	defer agent.processMu.Unlock()
	records := make([]processharness.CleanupRecord, 0, len(agent.processes))
	for _, process := range agent.processes {
		records = append(records, process.record)
	}
	return processharness.NewCleanupReceipt(records)
}
func (agent *Agent) ConfigPath() string                      { return agent.configPath }
func (agent *Agent) BinaryPath() string                      { return agent.binaryPath }
func (agent *Agent) LogPath() string                         { return agent.logPath }
func (agent *Agent) WorkspaceRoot() string                   { return agent.workspace.Root() }
func (agent *Agent) CleanupDone() <-chan struct{}            { return agent.cleanupDone }
func (agent *Agent) FMProducerObserver() *FMProducerObserver { return agent.fmObserver }
