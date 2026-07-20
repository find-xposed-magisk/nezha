//go:build agentcompat

package rpc

// SetIOStreamStateWaitObserverForAgentcompat installs a deterministic harness
// seam for observing that a waiter captured its notification channel.
func (s *NezhaHandler) SetIOStreamStateWaitObserverForAgentcompat(observer func()) {
	s.ioStreamMutex.Lock()
	defer s.ioStreamMutex.Unlock()
	s.ioStreamWaitLockedHook = observer
}
