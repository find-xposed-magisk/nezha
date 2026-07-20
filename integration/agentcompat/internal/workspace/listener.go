//go:build linux

package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type OwnedListener struct {
	file      *os.File
	address   string
	inode     uint64
	closeOnce sync.Once
	closeErr  error
}

type ListenerIdentity struct {
	Address string
	Inode   uint64
}

func (listener *OwnedListener) FileDescriptor() int { return int(listener.file.Fd()) }

func (listener *OwnedListener) Address() string { return listener.address }

func (listener *OwnedListener) Identity() ListenerIdentity {
	return ListenerIdentity{Address: listener.address, Inode: listener.inode}
}

func (listener *OwnedListener) ExtraFile() (*os.File, error) {
	descriptor, err := syscall.Dup(listener.FileDescriptor())
	if err != nil {
		return nil, fmt.Errorf("duplicate inherited listener FD: %w", err)
	}
	return os.NewFile(uintptr(descriptor), "agentcompat-listener"), nil
}

func (listener *OwnedListener) Close() error {
	listener.closeOnce.Do(func() {
		if err := listener.file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			listener.closeErr = fmt.Errorf("close owned listener: %w", err)
		}
	})
	return listener.closeErr
}

func socketInode(file *os.File) (uint64, error) {
	target, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(file.Fd())))
	if err != nil {
		return 0, fmt.Errorf("read listener FD link: %w", err)
	}
	if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
		return 0, errors.New("listener FD is not a socket")
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse listener inode: %w", err)
	}
	return inode, nil
}

func listenerInodePresent(inode uint64) (bool, error) {
	for _, path := range []string{"/proc/self/net/tcp", "/proc/self/net/tcp6"} {
		file, err := os.Open(path)
		if err != nil {
			return false, fmt.Errorf("open listener table %s: %w", path, err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 10 && fields[3] == "0A" && fields[9] == strconv.FormatUint(inode, 10) {
				_ = file.Close()
				return true, nil
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil || closeErr != nil {
			return false, errors.Join(scanErr, closeErr)
		}
	}
	return false, nil
}
