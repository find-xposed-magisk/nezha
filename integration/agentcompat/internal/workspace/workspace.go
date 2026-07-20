//go:build linux

package workspace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const defaultLogBytes = 1024 * 1024

type Workspace struct {
	root       string
	binDir     string
	logDir     string
	payloadDir string
	done       chan struct{}
	closeMu    sync.Mutex
	closed     bool
	closing    bool
	mu         sync.Mutex
	logs       []*LogFile
	listeners  []*OwnedListener
	pids       map[int]struct{}
	groups     map[int]struct{}
}

func New(ctx context.Context) (*Workspace, error) {
	root, err := os.MkdirTemp("", "nezha-agentcompat-")
	if err != nil {
		return nil, fmt.Errorf("create agent compatibility workspace: %w", err)
	}
	workspace := &Workspace{
		root:       root,
		binDir:     filepath.Join(root, "bin"),
		logDir:     filepath.Join(root, "logs"),
		payloadDir: filepath.Join(root, "payloads"),
		done:       make(chan struct{}),
		pids:       make(map[int]struct{}),
		groups:     make(map[int]struct{}),
	}
	for _, directory := range []string{workspace.binDir, workspace.logDir, workspace.payloadDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			_ = os.RemoveAll(root)
			return nil, fmt.Errorf("create workspace directory %s: %w", filepath.Base(directory), err)
		}
	}
	go workspace.closeOnCancellation(ctx)
	return workspace, nil
}

func (workspace *Workspace) closeOnCancellation(ctx context.Context) {
	select {
	case <-ctx.Done():
		_ = workspace.Close()
	case <-workspace.done:
	}
}

func (workspace *Workspace) Root() string { return workspace.root }

func (workspace *Workspace) BinaryPath(name string) (string, error) {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if err := workspace.requireOpen(); err != nil {
		return "", err
	}
	if err := validateLeafName(name); err != nil {
		return "", err
	}
	return filepath.Join(workspace.binDir, name), nil
}

func (workspace *Workspace) PayloadPath(name string) (string, error) {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if err := workspace.requireOpen(); err != nil {
		return "", err
	}
	if err := validateLeafName(name); err != nil {
		return "", err
	}
	return filepath.Join(workspace.payloadDir, name), nil
}

func validateLeafName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsRune(name, os.PathSeparator) {
		return errors.New("workspace name must be one path component")
	}
	return nil
}

func (workspace *Workspace) Log(name string) (*LogFile, error) {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if err := workspace.requireOpen(); err != nil {
		return nil, err
	}
	if err := validateLeafName(name); err != nil {
		return nil, err
	}
	logFile, err := newLogFile(filepath.Join(workspace.logDir, name+".log"), defaultLogBytes)
	if err != nil {
		return nil, err
	}
	workspace.mu.Lock()
	workspace.logs = append(workspace.logs, logFile)
	workspace.mu.Unlock()
	return logFile, nil
}

func (workspace *Workspace) AdoptListener(listener net.Listener) (*OwnedListener, error) {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if err := workspace.requireOpen(); err != nil {
		return nil, err
	}
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		return nil, errors.New("workspace listener must be TCP")
	}
	address, ok := tcpListener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return nil, errors.New("workspace listener must be loopback TCP")
	}
	file, err := tcpListener.File()
	if err != nil {
		return nil, fmt.Errorf("duplicate workspace listener: %w", err)
	}
	inode, err := socketInode(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := tcpListener.Close(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("transfer workspace listener ownership: %w", err)
	}
	owned := &OwnedListener{file: file, address: address.String(), inode: inode}
	workspace.mu.Lock()
	workspace.listeners = append(workspace.listeners, owned)
	workspace.mu.Unlock()
	return owned, nil
}

func (workspace *Workspace) TrackPID(pid int) error {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if err := workspace.requireOpen(); err != nil {
		return err
	}
	if pid < 1 {
		return errors.New("tracked PID must be positive")
	}
	workspace.mu.Lock()
	workspace.pids[pid] = struct{}{}
	workspace.mu.Unlock()
	return nil
}

func (workspace *Workspace) TrackProcessGroup(processGroupID int) error {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if err := workspace.requireOpen(); err != nil {
		return err
	}
	if processGroupID < 1 {
		return errors.New("tracked process group ID must be positive")
	}
	workspace.mu.Lock()
	workspace.groups[processGroupID] = struct{}{}
	workspace.mu.Unlock()
	return nil
}

func (workspace *Workspace) Close() error {
	workspace.closeMu.Lock()
	defer workspace.closeMu.Unlock()
	if workspace.closed {
		return nil
	}
	workspace.closing = true
	if err := workspace.close(); err != nil {
		workspace.closing = false
		return err
	}
	workspace.closed = true
	close(workspace.done)
	return nil
}

func (workspace *Workspace) requireOpen() error {
	if workspace.closed || workspace.closing {
		return errors.New("workspace is closing or closed")
	}
	return nil
}

func (workspace *Workspace) close() error {
	workspace.mu.Lock()
	logs := append([]*LogFile(nil), workspace.logs...)
	listeners := append([]*OwnedListener(nil), workspace.listeners...)
	pids := make([]int, 0, len(workspace.pids))
	for pid := range workspace.pids {
		pids = append(pids, pid)
	}
	groups := make([]int, 0, len(workspace.groups))
	for processGroupID := range workspace.groups {
		groups = append(groups, processGroupID)
	}
	workspace.mu.Unlock()

	var cleanupErrors []error
	for _, logFile := range logs {
		if err := logFile.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	for _, pid := range pids {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); err == nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("tracked PID %d remains", pid))
		} else if !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("inspect tracked PID %d: %w", pid, err))
		}
	}
	for _, processGroupID := range groups {
		if err := syscall.Kill(-processGroupID, 0); err == nil || errors.Is(err, syscall.EPERM) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("tracked process group %d remains", processGroupID))
		} else if !errors.Is(err, syscall.ESRCH) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("inspect tracked process group %d: %w", processGroupID, err))
		}
	}
	for _, listener := range listeners {
		present, err := listenerInodePresent(listener.inode)
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
		} else if present {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("listener %s inode %d remains", listener.address, listener.inode))
		}
	}
	if err := os.RemoveAll(workspace.payloadDir); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove workspace payloads: %w", err))
	} else if _, err := os.Stat(workspace.payloadDir); !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("workspace payload residue remains: %w", err))
	}
	if len(cleanupErrors) == 0 {
		if err := os.RemoveAll(workspace.root); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove workspace: %w", err))
		}
	}
	return errors.Join(cleanupErrors...)
}
