//go:build linux && agentcompat

package scenario

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	processharness "github.com/nezhahq/nezha/integration/agentcompat/internal/process"
)

type stressSQLiteHoldControlProbe struct {
	mu      sync.Mutex
	calls   []string
	started chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newStressSQLiteHoldControlProbe() *stressSQLiteHoldControlProbe {
	return &stressSQLiteHoldControlProbe{started: make(chan struct{}), done: make(chan struct{})}
}

func (probe *stressSQLiteHoldControlProbe) record(call string) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	probe.calls = append(probe.calls, call)
}

func (probe *stressSQLiteHoldControlProbe) ArmSQLiteHold(context.Context) (client.SQLiteHoldReceipt, error) {
	probe.record("arm")
	return client.SQLiteHoldReceipt{ID: "ERERERERERERERERERERERERERERERERERERERERERE", State: client.SQLiteHoldStateArmed}, nil
}

func (probe *stressSQLiteHoldControlProbe) WaitForSQLiteHold(ctx context.Context, receipt client.SQLiteHoldReceipt, target client.SQLiteHoldState) (client.SQLiteHoldReceipt, error) {
	if target == client.SQLiteHoldStateSelected {
		select {
		case <-probe.started:
		case <-ctx.Done():
			return client.SQLiteHoldReceipt{}, ctx.Err()
		}
	}
	probe.record("wait-" + string(target))
	receipt.State = target
	return receipt, nil
}

func (probe *stressSQLiteHoldControlProbe) ReleaseSQLiteHold(context.Context, client.SQLiteHoldReceipt) (client.SQLiteHoldReceipt, error) {
	probe.record("release")
	probe.once.Do(func() { close(probe.done) })
	return client.SQLiteHoldReceipt{State: client.SQLiteHoldStateReleased}, nil
}

func (probe *stressSQLiteHoldControlProbe) AbortSQLiteHold(context.Context, client.SQLiteHoldReceipt) (client.SQLiteHoldReceipt, error) {
	probe.record("abort")
	probe.once.Do(func() { close(probe.done) })
	return client.SQLiteHoldReceipt{State: client.SQLiteHoldStateAborted}, nil
}

type stressSQLiteJournalWatchProbe struct {
	control *stressSQLiteHoldControlProbe
}

func (watch stressSQLiteJournalWatchProbe) Wait(ctx context.Context) error {
	select {
	case <-watch.control.done:
		watch.control.record("watch-wait")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (watch stressSQLiteJournalWatchProbe) Close() error {
	watch.control.record("watch-close")
	return nil
}

func TestDrainStressSQLiteJournalOrdersHoldLifecycleBeforeCompletion(t *testing.T) {
	// Given
	control := newStressSQLiteHoldControlProbe()
	journalPath := filepath.Join(t.TempDir(), "dashboard.sqlite-journal")

	// When
	err := drainStressSQLiteJournal(t.Context(), control, func(ctx context.Context) error {
		control.record("writer-start")
		close(control.started)
		select {
		case <-control.done:
			control.record("writer-complete")
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, journalPath, func(path string) (stressSQLiteJournalWatch, error) {
		require.Equal(t, journalPath, path)
		control.record("watch-open")
		return stressSQLiteJournalWatchProbe{control: control}, nil
	})

	// Then
	require.NoError(t, err)
	control.mu.Lock()
	calls := append([]string(nil), control.calls...)
	control.mu.Unlock()
	require.Less(t, callIndex(calls, "arm"), callIndex(calls, "writer-start"))
	require.Less(t, callIndex(calls, "writer-start"), callIndex(calls, "wait-selected"))
	require.Less(t, callIndex(calls, "wait-selected"), callIndex(calls, "wait-finalizing"))
	require.Less(t, callIndex(calls, "wait-finalizing"), callIndex(calls, "watch-open"))
	require.Less(t, callIndex(calls, "watch-open"), callIndex(calls, "release"))
	require.Less(t, callIndex(calls, "release"), callIndex(calls, "watch-wait"))
	require.Less(t, callIndex(calls, "release"), callIndex(calls, "writer-complete"))
	require.Less(t, callIndex(calls, "watch-wait"), callIndex(calls, "watch-close"))
}

func TestDrainStressSQLiteJournalAbortsWriterWhenWatchCannotOpen(t *testing.T) {
	// Given
	control := newStressSQLiteHoldControlProbe()
	watchErr := errors.New("watch unavailable")

	// When
	err := drainStressSQLiteJournal(t.Context(), control, func(ctx context.Context) error {
		close(control.started)
		select {
		case <-control.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, filepath.Join(t.TempDir(), "dashboard.sqlite-journal"), func(string) (stressSQLiteJournalWatch, error) {
		return nil, watchErr
	})

	// Then
	require.ErrorIs(t, err, watchErr)
	control.mu.Lock()
	calls := append([]string(nil), control.calls...)
	control.mu.Unlock()
	require.Contains(t, calls, "abort")
}

func TestObserveStressDashboardSQLiteJournalRejectsFirstHeldSample(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "dashboard.sqlite-journal")
	require.NoError(t, os.WriteFile(path, []byte("journal"), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	observer := observeStressDashboardSQLiteJournal(path)

	// When
	heldErr := observer(t.Context(), processharness.Sample{PID: os.Getpid()})
	require.NoError(t, file.Close())
	releasedErr := observer(t.Context(), processharness.Sample{PID: os.Getpid()})

	// Then
	require.ErrorIs(t, heldErr, ErrStressSQLiteJournalNotDrained)
	require.NoError(t, releasedErr)
}

func callIndex(calls []string, wanted string) int {
	for index, call := range calls {
		if call == wanted {
			return index
		}
	}
	return len(calls)
}
