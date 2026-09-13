package tsdb

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testReadOnlyChecker struct {
	value atomic.Bool
}

func (c *testReadOnlyChecker) IsReadOnly() bool {
	return c.value.Load()
}

func newDiskGuardTestDB(t *testing.T) *TSDB {
	t.Helper()
	db, err := Open(&Config{
		DataPath:           filepath.Join(t.TempDir(), "tsdb"),
		RetentionDays:      1,
		MinFreeDiskSpaceGB: 1,
		DedupInterval:      time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func bufferedRows(w *bufferedWriter) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.buffer)
}

func TestReadOnlyStorageDropsWritesAndKeepsQueriesAvailable(t *testing.T) {
	db := newDiskGuardTestDB(t)
	checker := &testReadOnlyChecker{}
	db.readOnly = checker
	originalAddRows := db.addRowsFn
	var addRowsCalls atomic.Int32
	db.addRowsFn = func(rows []storage.MetricRow, precisionBits uint8) {
		addRowsCalls.Add(1)
		originalAddRows(rows, precisionBits)
	}

	firstTimestamp := time.Now().Add(-time.Minute)
	require.NoError(t, db.WriteServerMetrics(&ServerMetrics{
		ServerID:  1,
		Timestamp: firstTimestamp,
		CPU:       10,
	}))
	db.Flush()
	require.Equal(t, int32(1), addRowsCalls.Load())

	before, err := db.QueryServerMetrics(1, MetricServerCPU, Period1Day)
	require.NoError(t, err)
	require.NotEmpty(t, before)

	checker.value.Store(true)
	require.NoError(t, db.WriteServerMetrics(&ServerMetrics{
		ServerID:  1,
		Timestamp: firstTimestamp.Add(2 * time.Second),
		CPU:       20,
	}))
	assert.True(t, db.WritesPaused())
	assert.Zero(t, bufferedRows(db.writer), "read-only writes must not accumulate in memory")
	assert.Equal(t, int32(1), addRowsCalls.Load(), "read-only samples must not reach VictoriaMetrics")

	afterDrop, err := db.QueryServerMetrics(1, MetricServerCPU, Period1Day)
	require.NoError(t, err)
	assert.Equal(t, before, afterDrop, "read-only mode must preserve existing query access")

	checker.value.Store(false)
	require.NoError(t, db.WriteServerMetrics(&ServerMetrics{
		ServerID:  1,
		Timestamp: firstTimestamp.Add(4 * time.Second),
		CPU:       30,
	}))
	db.Flush()
	assert.False(t, db.WritesPaused())
	assert.Equal(t, int32(2), addRowsCalls.Load(), "writes must resume after storage leaves read-only mode")
}

func TestDiskFullAddRowsPanicPausesWrites(t *testing.T) {
	db := newDiskGuardTestDB(t)
	db.addRowsFn = func([]storage.MetricRow, uint8) {
		panic("write data: no space left on device")
	}

	require.NoError(t, db.WriteServerMetrics(&ServerMetrics{
		ServerID:  1,
		Timestamp: time.Now(),
		CPU:       10,
	}))
	assert.NotPanics(t, db.Flush)
	assert.True(t, db.WritesPaused())

	require.NoError(t, db.WriteServerMetrics(&ServerMetrics{
		ServerID:  1,
		Timestamp: time.Now().Add(time.Second),
		CPU:       20,
	}))
	assert.Zero(t, bufferedRows(db.writer))
}

func TestNonDiskStoragePanicIsNotHidden(t *testing.T) {
	db := newDiskGuardTestDB(t)
	db.addRowsFn = func([]storage.MetricRow, uint8) {
		panic("storage invariant failed")
	}

	require.NoError(t, db.WriteServerMetrics(&ServerMetrics{
		ServerID:  1,
		Timestamp: time.Now(),
		CPU:       10,
	}))
	assert.PanicsWithValue(t, "storage invariant failed", db.Flush)
}
