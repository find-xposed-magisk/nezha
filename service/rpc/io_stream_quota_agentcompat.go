//go:build agentcompat

package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type IOStreamQuotaProbeResult struct {
	UserAccepted            int
	UserRejected            int
	ServerAccepted          int
	ServerRejected          int
	TrackedStreams          int
	WaitForAgentWokeOnClose bool
	UserSlotReused          bool
	UserBoundaryError       error
	ServerBoundaryError     error
	Err                     error
}

func RunIOStreamQuotaProbe(ctx context.Context) IOStreamQuotaProbeResult {
	if err := ctx.Err(); err != nil {
		return IOStreamQuotaProbeResult{Err: err}
	}
	h := NewNezhaHandler()
	result := IOStreamQuotaProbeResult{}
	defer func() {
		h.ioStreamMutex.RLock()
		streamIDs := make([]string, 0, len(h.ioStreams))
		for streamID := range h.ioStreams {
			streamIDs = append(streamIDs, streamID)
		}
		h.ioStreamMutex.RUnlock()
		for _, streamID := range streamIDs {
			_ = h.CloseStream(streamID)
		}
	}()
	for i := 0; i < maxStreamsPerUser; i++ {
		if err := h.CreateStream(fmt.Sprintf("probe-user-%d", i), 101, uint64(i+1)); err != nil {
			result.Err = fmt.Errorf("create user stream %d: %w", i, err)
			return result
		}
		result.UserAccepted++
	}
	result.UserBoundaryError = h.CreateStream("probe-user-over", 101, 500)
	if !errors.Is(result.UserBoundaryError, ErrTooManyStreamsForUser) {
		result.Err = fmt.Errorf("user boundary returned %v", result.UserBoundaryError)
		return result
	}
	result.UserRejected = 1

	for i := 0; i < maxStreamsPerServer; i++ {
		if err := h.CreateStream(fmt.Sprintf("probe-server-%d", i), uint64(i+1000), 700); err != nil {
			result.Err = fmt.Errorf("create server stream %d: %w", i, err)
			return result
		}
		result.ServerAccepted++
	}
	result.ServerBoundaryError = h.CreateStream("probe-server-over", 2000, 700)
	if !errors.Is(result.ServerBoundaryError, ErrTooManyStreamsForServer) {
		result.Err = fmt.Errorf("server boundary returned %v", result.ServerBoundaryError)
		return result
	}
	result.ServerRejected = 1

	if err := h.CloseStream("probe-user-0"); err != nil {
		result.Err = fmt.Errorf("close stale user slot: %w", err)
		return result
	}
	if err := h.CreateStream("probe-user-reused", 101, 501); err != nil {
		result.Err = fmt.Errorf("reuse stale user slot: %w", err)
		return result
	}
	result.UserSlotReused = true

	if err := h.CreateStream("probe-wait", 0, 502); err != nil {
		result.Err = fmt.Errorf("create cancellation probe stream: %w", err)
		return result
	}
	waitStream, err := h.GetStream("probe-wait")
	if err != nil {
		result.Err = fmt.Errorf("get cancellation probe stream: %w", err)
		return result
	}
	waitResult := make(chan bool, 1)
	go func() {
		_, ok := h.WaitForAgent(ctx, "probe-wait", 30*time.Second)
		waitResult <- ok
	}()
	select {
	case <-waitStream.waitStartedCh:
	case <-ctx.Done():
		result.Err = ctx.Err()
		return result
	}
	if err := h.CloseStream("probe-wait"); err != nil {
		result.Err = fmt.Errorf("close cancellation probe stream: %w", err)
		return result
	}
	select {
	case ok := <-waitResult:
		result.WaitForAgentWokeOnClose = !ok
	case <-ctx.Done():
		result.Err = ctx.Err()
		return result
	}
	if !result.WaitForAgentWokeOnClose {
		result.Err = errors.New("WaitForAgent did not wake after stream close")
		return result
	}

	for i := 0; i < maxStreamsPerUser; i++ {
		if err := h.CloseStream(fmt.Sprintf("probe-user-%d", i)); err != nil {
			result.Err = fmt.Errorf("close user stream %d: %w", i, err)
			return result
		}
		if err := h.CloseStream(fmt.Sprintf("probe-user-%d", i)); err != nil {
			result.Err = fmt.Errorf("repeat close user stream %d: %w", i, err)
			return result
		}
	}
	if err := h.CloseStream("probe-user-reused"); err != nil {
		result.Err = fmt.Errorf("close reused user slot: %w", err)
		return result
	}
	for i := 0; i < maxStreamsPerServer; i++ {
		if err := h.CloseStream(fmt.Sprintf("probe-server-%d", i)); err != nil {
			result.Err = fmt.Errorf("close server stream %d: %w", i, err)
			return result
		}
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	h.ioStreamMutex.RLock()
	result.TrackedStreams = len(h.ioStreams)
	h.ioStreamMutex.RUnlock()
	return result
}
