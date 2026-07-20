package rpc

import (
	"context"
	"errors"
)

var ErrInvalidIOStreamStateExpectation = errors.New("invalid IOStream state expectation")

type IOStreamState struct {
	Count      int    `json:"count"`
	Generation uint64 `json:"generation"`
}

type IOStreamStateExpectation struct {
	// A pointer distinguishes an omitted count from an explicit zero count.
	ExpectedCount   *int   `json:"expected_count,omitempty"`
	PresentStreamID string `json:"present_stream_id,omitempty"`
	AbsentStreamID  string `json:"absent_stream_id,omitempty"`
}

func ExpectedIOStreamCount(count int) *int {
	return &count
}

func (s IOStreamStateExpectation) validate() error {
	if s.ExpectedCount == nil && s.PresentStreamID == "" && s.AbsentStreamID == "" {
		return ErrInvalidIOStreamStateExpectation
	}
	if s.ExpectedCount != nil && *s.ExpectedCount < 0 {
		return ErrInvalidIOStreamStateExpectation
	}
	if s.PresentStreamID != "" && s.PresentStreamID == s.AbsentStreamID {
		return ErrInvalidIOStreamStateExpectation
	}
	return nil
}

func (s *NezhaHandler) SnapshotIOStreamState() IOStreamState {
	s.ioStreamMutex.RLock()
	defer s.ioStreamMutex.RUnlock()
	return s.snapshotIOStreamStateLocked()
}

func (s *NezhaHandler) snapshotIOStreamStateLocked() IOStreamState {
	return IOStreamState{Count: len(s.ioStreams), Generation: s.ioStreamGeneration}
}

func (s *NezhaHandler) ioStreamStateExpectationSatisfiedLocked(expectation IOStreamStateExpectation) bool {
	if expectation.ExpectedCount != nil && len(s.ioStreams) != *expectation.ExpectedCount {
		return false
	}
	if expectation.PresentStreamID != "" {
		if _, exists := s.ioStreams[expectation.PresentStreamID]; !exists {
			return false
		}
	}
	if expectation.AbsentStreamID != "" {
		if _, exists := s.ioStreams[expectation.AbsentStreamID]; exists {
			return false
		}
	}
	return true
}

func (s *NezhaHandler) WaitForIOStreamState(ctx context.Context, expectation IOStreamStateExpectation) (IOStreamState, error) {
	if err := expectation.validate(); err != nil {
		return IOStreamState{}, err
	}
	for {
		s.ioStreamMutex.RLock()
		notify := s.ioStreamNotify
		state := s.snapshotIOStreamStateLocked()
		satisfied := s.ioStreamStateExpectationSatisfiedLocked(expectation)
		observer := s.ioStreamWaitLockedHook
		s.ioStreamMutex.RUnlock()
		if observer != nil {
			observer()
		}
		if satisfied {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return IOStreamState{}, ctx.Err()
		case <-notify:
		}
	}
}

func (s *NezhaHandler) publishIOStreamStateChangeLocked() {
	s.ioStreamGeneration++
	close(s.ioStreamNotify)
	s.ioStreamNotify = make(chan struct{})
}
