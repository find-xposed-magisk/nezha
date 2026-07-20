//go:build linux

package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

const workspaceTruncationMarker = "[TRUNCATED]\n"

type LogFile struct {
	file      *os.File
	maxBytes  int
	written   int
	pending   []byte
	dropLine  bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
	mu        sync.Mutex
}

func newLogFile(path string, maxBytes int) (*LogFile, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create workspace log: %w", err)
	}
	return &LogFile{file: file, maxBytes: maxBytes}, nil
}

func (logFile *LogFile) Name() string { return logFile.file.Name() }

func (logFile *LogFile) WriteString(value string) (int, error) {
	return logFile.Write([]byte(value))
}

func (logFile *LogFile) Write(data []byte) (int, error) {
	logFile.mu.Lock()
	defer logFile.mu.Unlock()
	if logFile.closed {
		return 0, errors.New("write closed workspace log")
	}
	inputLength := len(data)
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			logFile.appendFragment(data)
			break
		}
		logFile.appendFragment(data[:newline+1])
		if err := logFile.flushLine(); err != nil {
			return 0, err
		}
		data = data[newline+1:]
	}
	return inputLength, nil
}

func (logFile *LogFile) appendFragment(fragment []byte) {
	if logFile.dropLine {
		return
	}
	if len(logFile.pending)+len(fragment) > logFile.maxBytes {
		logFile.pending = nil
		logFile.dropLine = true
		return
	}
	logFile.pending = append(logFile.pending, fragment...)
}

func (logFile *LogFile) flushLine() error {
	if logFile.dropLine {
		logFile.dropLine = false
		return logFile.writeBounded(workspaceTruncationMarker)
	}
	redacted := evidence.Redact(string(logFile.pending))
	logFile.pending = nil
	if len(redacted) > logFile.maxBytes-logFile.written {
		return logFile.writeBounded(workspaceTruncationMarker)
	}
	return logFile.writeBounded(redacted)
}

func (logFile *LogFile) writeBounded(value string) error {
	remaining := logFile.maxBytes - logFile.written
	if remaining <= 0 || value == "" {
		return nil
	}
	if len(value) > remaining {
		value = value[:remaining]
	}
	written, err := logFile.file.WriteString(value)
	logFile.written += written
	if err != nil {
		return fmt.Errorf("write workspace log: %w", err)
	}
	return nil
}

func (logFile *LogFile) Close() error {
	logFile.closeOnce.Do(func() {
		logFile.mu.Lock()
		defer logFile.mu.Unlock()
		if len(logFile.pending) > 0 || logFile.dropLine {
			logFile.closeErr = logFile.flushLine()
		}
		logFile.closed = true
		logFile.closeErr = errors.Join(logFile.closeErr, logFile.file.Close())
	})
	return logFile.closeErr
}
