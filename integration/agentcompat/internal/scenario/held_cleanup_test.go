//go:build linux

package scenario

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeldCleanupStackRunsActionsInReverseOrderAndJoinsErrors(t *testing.T) {
	stack := newHeldCleanupStack()
	var order []string
	firstErr := errors.New("first cleanup failure")
	secondErr := errors.New("second cleanup failure")
	for _, action := range []heldCleanupAction{
		{name: "first", cleanup: func(context.Context) error {
			order = append(order, "first")
			return firstErr
		}},
		{name: "second", cleanup: func(context.Context) error {
			order = append(order, "second")
			return secondErr
		}},
	} {
		if err := stack.Push(action); err != nil {
			t.Fatal(err)
		}
	}
	err := stack.Run(context.Background())
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Fatalf("joined cleanup error = %v", err)
	}
	if !reflect.DeepEqual(order, []string{"second", "first"}) {
		t.Fatalf("cleanup order = %v", order)
	}
}

func TestHeldTerminalCleanupOrderCancelsBeforeWaitingForAbsence(t *testing.T) {
	// Given
	stack := newHeldCleanupStack()
	var order []string
	for _, action := range []heldCleanupAction{
		{name: "unregister", cleanup: func(context.Context) error { order = append(order, "unregister"); return nil }},
		{name: "absence", cleanup: func(context.Context) error { order = append(order, "absence"); return nil }},
		{name: "cancel", cleanup: func(context.Context) error { order = append(order, "cancel"); return nil }},
		{name: "close", cleanup: func(context.Context) error { order = append(order, "close"); return nil }},
		{name: "stop", cleanup: func(context.Context) error { order = append(order, "stop"); return nil }},
		{name: "await", cleanup: func(context.Context) error { order = append(order, "await"); return nil }},
		{name: "release", cleanup: func(context.Context) error { order = append(order, "release"); return nil }},
	} {
		if err := stack.Push(action); err != nil {
			t.Fatal(err)
		}
	}

	// When
	require.NoError(t, stack.Run(context.Background()))

	// Then
	require.Equal(t, []string{"release", "await", "stop", "close", "cancel", "absence", "unregister"}, order)
}

func TestHeldFMCleanupOrderStopsTransportCancelsThenProvesAbsenceBeforeUnregister(t *testing.T) {
	// Given
	stack := newHeldCleanupStack()
	var order []string
	for _, action := range []heldCleanupAction{
		{name: "remove fixture", cleanup: func(context.Context) error { order = append(order, "fixture"); return nil }},
		{name: "unregister", cleanup: func(context.Context) error { order = append(order, "unregister"); return nil }},
		{name: "absence", cleanup: func(context.Context) error { order = append(order, "absence"); return nil }},
		{name: "cancel", cleanup: func(context.Context) error { order = append(order, "cancel"); return nil }},
		{name: "transport", cleanup: func(context.Context) error { order = append(order, "transport"); return nil }},
	} {
		require.NoError(t, stack.Push(action))
	}

	// When
	err := stack.Run(context.Background())

	// Then
	require.NoError(t, err)
	require.Equal(t, []string{"transport", "cancel", "absence", "unregister", "fixture"}, order)
}

func TestHeldCleanupStackRunsEveryActionAndJoinsAllErrors(t *testing.T) {
	// Given
	stack := newHeldCleanupStack()
	firstErr := errors.New("first")
	secondErr := errors.New("second")
	thirdErr := errors.New("third")
	for name, actionErr := range map[string]error{"first": firstErr, "second": secondErr, "third": thirdErr} {
		require.NoError(t, stack.Push(heldCleanupAction{name: name, cleanup: func(context.Context) error { return actionErr }}))
	}

	// When
	err := stack.Run(context.Background())

	// Then
	require.ErrorIs(t, err, firstErr)
	require.ErrorIs(t, err, secondErr)
	require.ErrorIs(t, err, thirdErr)
}

func TestHeldLegacyFMCapabilityCleanupWiringRunsAllActionsInContractOrder(t *testing.T) {
	// Given
	stack := newHeldCleanupStack()
	var order []string
	originalErr := errors.New("original")
	absenceErr := errors.New("absence")
	cleanup := heldLegacyFMCapabilityCleanup{
		Unregister: func(context.Context) error { order = append(order, "unregister"); return nil },
		Absence:    func(context.Context) error { order = append(order, "absence"); return absenceErr },
		Cancel:     func(context.Context) error { order = append(order, "cancel"); return originalErr },
	}
	require.NoError(t, pushHeldLegacyFMCapabilityCleanup(stack, cleanup))

	// When
	err := errors.Join(originalErr, stack.Run(context.Background()))

	// Then
	require.Equal(t, []string{"cancel", "absence", "unregister"}, order)
	require.ErrorIs(t, err, originalErr)
	require.ErrorIs(t, err, absenceErr)
}

func TestHeldTerminalCleanupGraceTimeoutLeavesFallbackBudget(t *testing.T) {
	stack := newHeldCleanupStack()
	graceReturned := make(chan struct{})
	fallbackRan := make(chan struct{})
	capabilityCleanupRan := make(chan struct{})

	require.NoError(t, stack.Push(heldCleanupAction{name: "capability cleanup", cleanup: func(context.Context) error {
		close(capabilityCleanupRan)
		return nil
	}}))
	require.NoError(t, stack.Push(heldCleanupAction{name: "fallback", cleanup: func(context.Context) error {
		close(fallbackRan)
		return nil
	}}))
	require.NoError(t, stack.Push(heldCleanupAction{name: "graceful wait", cleanup: func(ctx context.Context) error {
		<-ctx.Done()
		close(graceReturned)
		return ctx.Err()
	}}))

	cleanupContext, cancel := context.WithTimeout(context.Background(), 2*heldTerminalGracePeriod)
	defer cancel()
	err := stack.Run(cleanupContext)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	<-graceReturned
	<-fallbackRan
	<-capabilityCleanupRan
}

func TestHeldCleanupStackRejectsInvalidAndLateActions(t *testing.T) {
	stack := newHeldCleanupStack()
	if err := stack.Push(heldCleanupAction{}); !errors.Is(err, ErrInvalidHeldCleanupAction) {
		t.Fatalf("invalid push error = %v", err)
	}
	if err := stack.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := stack.Push(heldCleanupAction{name: "late", cleanup: func(context.Context) error { return nil }}); !errors.Is(err, ErrHeldCleanupClosed) {
		t.Fatalf("late push error = %v", err)
	}
	if err := stack.Run(context.Background()); !errors.Is(err, ErrHeldCleanupClosed) {
		t.Fatalf("second Run error = %v", err)
	}
}
