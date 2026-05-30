package rpc

import (
	"sync"
	"sync/atomic"
	"testing"

	pb "github.com/nezhahq/nezha/proto"
)

// updateConfig has no serialization, so two concurrent admin PATCH /setting
// requests can both flip EnableMCP true->false and both invoke
// CancelAllMCPInflight concurrently. The sweep must close each entry's cancel
// channel at most once; a non-atomic check-then-close double-closes the same
// channel and panics, crashing the dashboard.
func TestCancelAllMCPInflight_ConcurrentSweepsDoNotDoubleClose(t *testing.T) {
	mcpInflight.Range(func(key, _ any) bool {
		mcpInflight.Delete(key)
		return true
	})
	t.Cleanup(func() {
		mcpInflight.Range(func(key, _ any) bool {
			mcpInflight.Delete(key)
			return true
		})
	})

	const entries = 256
	for i := 0; i < entries; i++ {
		mcpInflight.Store(uint64(i+1), &mcpInflightEntry{
			serverID:  uint64(i + 1),
			result:    make(chan *pb.TaskResult, 1),
			cancel:    make(chan struct{}),
			cancelled: new(atomic.Bool),
		})
	}

	const sweepers = 8
	var wg sync.WaitGroup
	wg.Add(sweepers)
	for i := 0; i < sweepers; i++ {
		go func() {
			defer wg.Done()
			// A double-close inside CancelAllMCPInflight panics here and
			// fails the test (panic in a goroutine aborts the test binary).
			CancelAllMCPInflight()
		}()
	}
	wg.Wait()

	mcpInflight.Range(func(key, _ any) bool {
		t.Fatalf("inflight entry %v survived the sweep", key)
		return false
	})
}
