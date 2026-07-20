//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type Sample struct {
	PID               int    `json:"pid"`
	RSSBytes          uint64 `json:"rss_bytes"`
	DescendantPIDs    []int  `json:"descendant_pids"`
	DescendantCount   int    `json:"descendant_count"`
	NonStdioFDCount   int    `json:"non_stdio_fd_count"`
	TCPListenerCount  int    `json:"tcp_listener_count"`
	TCP6ListenerCount int    `json:"tcp6_listener_count"`
}

type Window struct {
	PID     int      `json:"pid"`
	Samples []Sample `json:"samples"`
}

type WindowSpec struct {
	PID             int
	Interval        time.Duration
	AllowTerminated bool
	ObserveSample   func(context.Context, Sample) error
}

func SampleProcess(pid int) (Sample, error) {
	rssBytes, err := readRSSBytes(pid)
	if err != nil {
		return Sample{}, err
	}
	descendants, err := descendantPIDs(pid)
	if err != nil {
		return Sample{}, err
	}
	fdCount, socketInodes, err := processFDs(pid)
	if err != nil {
		return Sample{}, err
	}
	tcpListeners, err := listeningSocketInodes(pid, "tcp")
	if err != nil {
		return Sample{}, err
	}
	tcp6Listeners, err := listeningSocketInodes(pid, "tcp6")
	if err != nil {
		return Sample{}, err
	}
	return Sample{
		PID:               pid,
		RSSBytes:          rssBytes,
		DescendantPIDs:    descendants,
		DescendantCount:   len(descendants),
		NonStdioFDCount:   fdCount,
		TCPListenerCount:  intersectionCount(socketInodes, tcpListeners),
		TCP6ListenerCount: intersectionCount(socketInodes, tcp6Listeners),
	}, nil
}

func SampleWindow(ctx context.Context, spec WindowSpec) (Window, error) {
	if spec.PID < 1 || spec.Interval <= 0 {
		return Window{}, errors.New("invalid sample window specification")
	}
	window := Window{PID: spec.PID, Samples: make([]Sample, 0, contract.ResourceSampleCount)}
	for index := 0; index < contract.ResourceSampleCount; index++ {
		if index > 0 {
			timer := time.NewTimer(spec.Interval)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return Window{}, ctx.Err()
			}
		}
		sample, err := SampleProcess(spec.PID)
		if err != nil {
			if spec.AllowTerminated && os.IsNotExist(err) {
				return window, nil
			}
			return Window{}, fmt.Errorf("sample %d of PID %d: %w", index+1, spec.PID, err)
		}
		window.Samples = append(window.Samples, sample)
		if spec.ObserveSample != nil {
			if err := spec.ObserveSample(ctx, sample); err != nil {
				return window, err
			}
		}
	}
	return window, nil
}

func intersectionCount(left, right map[uint64]struct{}) int {
	count := 0
	for value := range left {
		if _, exists := right[value]; exists {
			count++
		}
	}
	return count
}
