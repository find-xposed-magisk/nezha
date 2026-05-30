package rpc

import (
	"io"
	"sync"
	"testing"
	"time"
)

// nopRWC is a minimal io.ReadWriteCloser used for race tests; Close is a
// no-op so the racer goroutines do not panic on shared state.
type nopRWC struct{}

func (nopRWC) Read(p []byte) (int, error)  { return 0, io.EOF }
func (nopRWC) Write(p []byte) (int, error) { return len(p), nil }
func (nopRWC) Close() error                { return nil }

// H10 regression: UserConnected/AgentConnected mutate stream.userIo /
// stream.agentIo without holding ioStreamMutex, while WaitForAgent /
// RevokeStreamsForPurpose / RevokeStreamsForServer read & close the same
// fields under the lock. The go race detector catches it deterministically
// under -race; without the fix this test fails.
func TestIOStream_AgentConnectedIsRaceFreeUnderLock(t *testing.T) {
	h := NewNezhaHandler()
	const streamId = "race-test"
	h.CreateStream(streamId, 1, 1)
	t.Cleanup(func() { _ = h.CloseStream(streamId) })

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		// repeatedly attach an agent
		for i := 0; i < 200; i++ {
			_ = h.AgentConnected(streamId, nopRWC{})
		}
	}()

	go func() {
		defer wg.Done()
		// concurrently attach a user
		for i := 0; i < 200; i++ {
			_ = h.UserConnected(streamId, nopRWC{})
		}
	}()

	go func() {
		defer wg.Done()
		// Revoker takes the write lock and reads the same userIo/agentIo
		// fields the unsynchronised writers above are setting. Use a real
		// targetServerID (1) so RevokeStreamsForServer actually inspects
		// the entry's userIo/agentIo before deleting.
		for i := 0; i < 200; i++ {
			h.RevokeStreamsForServer(1)
			h.CreateStream(streamId, 1, 1)
		}
	}()

	wg.Wait()
}

// StartStream reads stream.userIo/agentIo while it waits for both endpoints.
// Those reads must be lock-protected against the concurrent writes done by
// UserConnected/AgentConnected; otherwise -race flags the data race on the
// interface fields.
func TestIOStream_StartStreamReadsAreRaceFree(t *testing.T) {
	h := NewNezhaHandler()
	const streamId = "startstream-race"
	h.CreateStream(streamId, 2, 2)
	t.Cleanup(func() { _ = h.CloseStream(streamId) })

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		_ = h.StartStream(streamId, 50*time.Millisecond)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		_ = h.AgentConnected(streamId, nopRWC{})
	}()
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		_ = h.UserConnected(streamId, nopRWC{})
	}()

	wg.Wait()
}
