//go:build linux

package process

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/evidence"
)

const truncationMarker = "[TRUNCATED]\n"

type boundedLog struct {
	mu          sync.Mutex
	destination io.Writer
	maxBytes    int
	written     int
	pending     []byte
	dropLine    bool
	truncated   bool
	closed      bool
	onLine      func(string)
	writeErr    error
}

func newBoundedLog(destination io.Writer, maxBytes int, onLine func(string)) *boundedLog {
	return &boundedLog{destination: destination, maxBytes: maxBytes, onLine: onLine}
}

func (log *boundedLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return 0, errors.New("write closed process log")
	}
	inputLength := len(data)
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			log.appendFragment(data)
			break
		}
		log.appendFragment(data[:newline+1])
		if err := log.flushLine(); err != nil {
			return 0, err
		}
		data = data[newline+1:]
	}
	return inputLength, nil
}

func (log *boundedLog) appendFragment(fragment []byte) {
	if log.dropLine {
		return
	}
	if len(log.pending)+len(fragment) > log.maxBytes {
		log.pending = nil
		log.dropLine = true
		log.truncated = true
		return
	}
	log.pending = append(log.pending, fragment...)
}

func (log *boundedLog) flushLine() error {
	if log.dropLine {
		log.dropLine = false
		return log.writeMarker()
	}
	redacted := evidence.Redact(string(log.pending))
	log.pending = nil
	if log.onLine != nil {
		log.onLine(redacted)
	}
	if len(redacted) > log.maxBytes-log.written {
		log.truncated = true
		return log.writeMarker()
	}
	if log.destination != nil && redacted != "" {
		written, err := io.WriteString(log.destination, redacted)
		log.written += written
		if err != nil {
			log.writeErr = fmt.Errorf("write process log: %w", err)
			return log.writeErr
		}
	}
	return nil
}

func (log *boundedLog) writeMarker() error {
	if log.destination == nil || log.written >= log.maxBytes {
		return nil
	}
	marker := truncationMarker
	if len(marker) > log.maxBytes-log.written {
		marker = marker[:log.maxBytes-log.written]
	}
	written, err := io.WriteString(log.destination, marker)
	log.written += written
	if err != nil {
		log.writeErr = fmt.Errorf("write process log marker: %w", err)
		return log.writeErr
	}
	return nil
}

func (log *boundedLog) Close() {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.closed {
		return
	}
	if len(log.pending) > 0 || log.dropLine {
		_ = log.flushLine()
	}
	log.closed = true
}

func (log *boundedLog) Truncated() bool {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.truncated
}
