package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWaitForIOStreamStateRejectsZeroValueExpectation(t *testing.T) {
	handler := NewNezhaHandler()
	if _, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{}); !errors.Is(err, ErrInvalidIOStreamStateExpectation) {
		t.Fatalf("zero-value expectation error: %v", err)
	}
}

func TestWaitForIOStreamStateAcceptsExplicitZeroCount(t *testing.T) {
	handler := NewNezhaHandler()
	state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0)})
	if err != nil {
		t.Fatal(err)
	}
	if state != (IOStreamState{}) {
		t.Fatalf("explicit zero state: %+v", state)
	}
}

func TestWaitForIOStreamStateAcceptsPresentOnlyExpectation(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("present-only", 1, 1); err != nil {
		t.Fatal(err)
	}
	state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{PresentStreamID: "present-only"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != 1 || state.Generation != 1 {
		t.Fatalf("present-only state: %+v", state)
	}
}

func TestWaitForIOStreamStateRejectsSamePresentAndAbsentID(t *testing.T) {
	handler := NewNezhaHandler()
	_, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{PresentStreamID: "same", AbsentStreamID: "same"})
	if !errors.Is(err, ErrInvalidIOStreamStateExpectation) {
		t.Fatalf("same identity expectation error: %v", err)
	}
}

func TestWaitForIOStreamStateRequiresAllSpecifiedConditions(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("present", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := handler.CreateStream("other", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := handler.CreateStream("absent", 1, 3); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := handler.WaitForIOStreamState(ctx, IOStreamStateExpectation{
		ExpectedCount:   ExpectedIOStreamCount(2),
		PresentStreamID: "present",
		AbsentStreamID:  "absent",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("combined expectation cancellation: %v", err)
	}
}

func TestWaitForIOStreamStateAbsenceOnlyIgnoresUnrelatedStreams(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("unrelated", 1, 1); err != nil {
		t.Fatal(err)
	}
	state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{AbsentStreamID: "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != 1 || state.Generation != 1 {
		t.Fatalf("absence-only state: %+v", state)
	}
}

func TestWaitForIOStreamStateRejectsNegativeCountWithoutPrivateID(t *testing.T) {
	handler := NewNezhaHandler()
	_, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(-1), AbsentStreamID: "private-stream-id"})
	if !errors.Is(err, ErrInvalidIOStreamStateExpectation) {
		t.Fatalf("negative expectation error: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "private-stream-id") {
		t.Fatalf("private stream ID leaked: %v", err)
	}
}

func TestWaitForIOStreamStateRequiresCombinedCountAndAbsence(t *testing.T) {
	handler := NewNezhaHandler()
	if err := handler.CreateStream("present", 1, 1); err != nil {
		t.Fatal(err)
	}
	state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1), AbsentStreamID: "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != 1 || state.Generation != 1 {
		t.Fatalf("combined expectation state: %+v", state)
	}
}

func TestWaitForIOStreamStateCancellationRemainsValidForUnsatisfiedExpectation(t *testing.T) {
	handler := NewNezhaHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := handler.WaitForIOStreamState(ctx, IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
}

func TestWaitForIOStreamStateAlreadySatisfiedReturnsSnapshot(t *testing.T) {
	handler := NewNezhaHandler()
	state, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(0)})
	if err != nil {
		t.Fatal(err)
	}
	if state != (IOStreamState{}) {
		t.Fatalf("already satisfied state: %+v", state)
	}
}

func TestWaitForIOStreamStateRejectsInvalidAndHonorsCancellation(t *testing.T) {
	handler := NewNezhaHandler()
	if _, err := handler.WaitForIOStreamState(context.Background(), IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(-1)}); !errors.Is(err, ErrInvalidIOStreamStateExpectation) {
		t.Fatalf("invalid count error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := handler.WaitForIOStreamState(ctx, IOStreamStateExpectation{ExpectedCount: ExpectedIOStreamCount(1)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
}
