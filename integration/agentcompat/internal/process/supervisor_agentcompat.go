//go:build linux && agentcompat

package process

func (supervisor *Supervisor) CleanupDoneForTest() <-chan struct{} {
	return supervisor.cleanupDone
}
