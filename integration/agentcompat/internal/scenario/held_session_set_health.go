//go:build linux

package scenario

import "context"

func (set *heldSessionSet) startHealthWatchers() {
	set.healthEvents = make(chan heldHealthMessage, len(set.sessions))
	set.healthSnapshots = make([]chan heldHealthSnapshotRequest, len(set.sessions))
	set.healthWatcherDone = make([]chan struct{}, len(set.sessions))
	for index := range set.sessions {
		set.healthSnapshots[index] = make(chan heldHealthSnapshotRequest)
		set.healthWatcherDone[index] = make(chan struct{})
	}
	set.healthCoordinatorWG.Add(1)
	go set.runHealthCoordinator()
	set.healthWG.Add(len(set.sessions))
	for index, session := range set.sessions {
		go set.watchHealth(index, session)
	}
}

func (set *heldSessionSet) markHealthCoordinatorDone() {
	set.healthCoordinatorDoneOnce.Do(func() { close(set.coordinatorDone) })
}

func (set *heldSessionSet) WaitHealthy(ctx context.Context) error {
	select {
	case <-set.healthDone:
		return set.retainedHealthError()
	case <-set.closeDone:
		return set.retainedHealthError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (set *heldSessionSet) Done() <-chan struct{} { return set.healthDone }

func (set *heldSessionSet) beginOwnedClose() {
	set.healthMu.Lock()
	set.closing = true
	set.healthMu.Unlock()
}

func (set *heldSessionSet) retainedHealthError() error {
	set.healthMu.Lock()
	defer set.healthMu.Unlock()
	return set.healthError
}

func (set *heldSessionSet) stopHealthWatchers() {
	set.healthStopOnce.Do(func() { close(set.healthStop) })
}
