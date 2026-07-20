//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

type FMProducerSample struct {
	RunID     string `json:"run_id"`
	AgentUUID string `json:"agent_uuid"`
	SessionID string `json:"session_id"`
	Phase     string `json:"phase"`
	Active    int64  `json:"active"`
}

type FMProducerObserver struct {
	listener net.Listener
	samples  chan FMProducerSample
	done     chan struct{}
	once     sync.Once
}

func newFMProducerObserver(socketPath string) (*FMProducerObserver, error) {
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen for FM producer observations: %w", err)
	}
	observer := &FMProducerObserver{listener: listener, samples: make(chan FMProducerSample, 16), done: make(chan struct{})}
	go observer.accept()
	return observer, nil
}

func (observer *FMProducerObserver) accept() {
	defer close(observer.done)
	for {
		connection, err := observer.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		var sample FMProducerSample
		err = json.NewDecoder(connection).Decode(&sample)
		_ = connection.Close()
		if err == nil {
			observer.samples <- sample
		}
	}
}

func (observer *FMProducerObserver) Await(ctx context.Context, match func(FMProducerSample) bool) (FMProducerSample, error) {
	for {
		select {
		case sample := <-observer.samples:
			if match(sample) {
				return sample, nil
			}
		case <-ctx.Done():
			return FMProducerSample{}, ctx.Err()
		}
	}
}

func (observer *FMProducerObserver) Close() error {
	var closeErr error
	observer.once.Do(func() {
		closeErr = observer.listener.Close()
		<-observer.done
	})
	return closeErr
}

func fmObserverSocketPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, "fm-observer.sock")
}

func removeFMObserverSocket(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
