//go:build linux

package scenario

import "errors"

const heldSessionSetHealthMemberCount = 12

var ErrHeldSessionSetHealthProtocol = errors.New("held session set health protocol failed")

type heldHealthEvent struct {
	index int
	err   error
}

type heldHealthSnapshotRequest struct {
	epoch int
}

type heldHealthSnapshot struct {
	epoch int
	index int
	event *heldHealthEvent
}

type heldHealthMessage struct {
	epoch    int
	event    *heldHealthEvent
	snapshot *heldHealthSnapshot
}

type heldHealthShutdownRequest struct {
	ack chan struct{}
}

type heldHealthEpochProtocol struct {
	epoch     int
	seen      [heldSessionSetHealthMemberCount]bool
	replies   int
	err       error
	committed bool
}

func newHeldHealthEpochProtocol(epoch int) *heldHealthEpochProtocol {
	return &heldHealthEpochProtocol{epoch: epoch}
}

func (protocol *heldHealthEpochProtocol) accept(snapshot heldHealthSnapshot) {
	if protocol.err != nil || protocol.committed {
		return
	}
	if snapshot.epoch != protocol.epoch || snapshot.index < 0 || snapshot.index >= heldSessionSetHealthMemberCount || protocol.seen[snapshot.index] {
		protocol.err = ErrHeldSessionSetHealthProtocol
		return
	}
	protocol.seen[snapshot.index] = true
	protocol.replies++
	if protocol.replies == heldSessionSetHealthMemberCount {
		protocol.committed = true
	}
}

func (set *heldSessionSet) watchHealth(index int, session heldSession) {
	defer set.healthWG.Done()
	defer close(set.healthWatcherDone[index])
	var cached *heldHealthEvent
	eventSent := false
	closureObserved := false
	for {
		select {
		case <-session.Done():
			if !closureObserved && set.healthClosureObservedHook != nil {
				set.healthClosureObservedHook(index)
				closureObserved = true
			}
			if cached == nil {
				cached = heldSessionHealthEvent(index, session)
			}
			if !eventSent {
				select {
				case set.healthEvents <- heldHealthMessage{epoch: 1, event: cached}:
					if set.healthEventHook != nil {
						set.healthEventHook(heldHealthMessage{epoch: 1, event: cached})
					}
					eventSent = true
				case <-set.healthStop:
					return
				}
			}
		case request := <-set.healthSnapshots[index]:
			if set.healthSnapshotRequestHook != nil {
				set.healthSnapshotRequestHook(index)
			}
			select {
			case <-session.Done():
				if !closureObserved && set.healthClosureObservedHook != nil {
					set.healthClosureObservedHook(index)
					closureObserved = true
				}
				cached = heldSessionHealthEvent(index, session)
			default:
			}
			message := &heldHealthSnapshot{epoch: request.epoch, index: index, event: cached}
			if set.healthSnapshotOverrideHook != nil {
				if override := set.healthSnapshotOverrideHook(index, request); override != nil {
					message = override
				}
			}
			if set.healthSnapshotSendHook != nil {
				set.healthSnapshotSendHook(index)
			}
			select {
			case set.healthEvents <- heldHealthMessage{epoch: request.epoch, snapshot: message}:
				if set.healthEventHook != nil {
					set.healthEventHook(heldHealthMessage{epoch: request.epoch, snapshot: message})
				}
			case <-set.healthStop:
				return
			}
			if set.healthSnapshotReplyHook != nil {
				set.healthSnapshotReplyHook(index)
			}
		case <-set.healthStop:
			return
		}
	}
}

func heldSessionHealthEvent(index int, session heldSession) *heldHealthEvent {
	errorValue := redactHeldSessionSetHealthError(session.CloseResult())
	if errorValue == nil {
		errorValue = redactHeldSessionSetHealthError(ErrHeldSessionPrematureClose)
	}
	return &heldHealthEvent{index: index, err: errorValue}
}

func (set *heldSessionSet) runHealthCoordinator() {
	defer set.healthCoordinatorWG.Done()
	defer set.markHealthCoordinatorDone()
	for {
		select {
		case message := <-set.healthEvents:
			if message.event != nil {
				set.commitHealthEpoch(*message.event, 1, nil)
				return
			}
		case request := <-set.healthShutdownRequests:
			if set.healthShutdownAcceptedHook != nil {
				set.healthShutdownAcceptedHook()
			}
			set.commitHealthEpoch(heldHealthEvent{index: heldSessionSetHealthMemberCount}, 1, request.ack)
			return
		case <-set.healthStop:
			return
		}
	}
}

func (set *heldSessionSet) commitHealthEpoch(trigger heldHealthEvent, epoch int, shutdownAck chan struct{}) {
	shutdownAcknowledgement := shutdownAck
	acknowledgeShutdown := func() {
		if shutdownAcknowledgement != nil {
			close(shutdownAcknowledgement)
			if set.healthShutdownAcknowledgedHook != nil {
				set.healthShutdownAcknowledgedHook()
			}
			shutdownAcknowledgement = nil
		}
	}
	for index := range set.healthSnapshots {
		select {
		case set.healthSnapshots[index] <- heldHealthSnapshotRequest{epoch: epoch}:
		case <-set.healthStop:
			acknowledgeShutdown()
			return
		}
	}
	best := trigger
	protocol := newHeldHealthEpochProtocol(epoch)
	acceptedSnapshots := [heldSessionSetHealthMemberCount]bool{}
	for protocol.replies < len(set.healthSnapshots) {
		select {
		case message := <-set.healthEvents:
			if message.event != nil {
				if shutdownAcknowledgement != nil && !acceptedSnapshots[message.event.index] && message.event.index < best.index {
					best = *message.event
				}
				continue
			}
			if message.snapshot == nil {
				continue
			}
			protocol.accept(*message.snapshot)
			if protocol.err != nil {
				set.commitHealthError(redactHeldSessionSetHealthError(protocol.err))
				set.stopHealthWatchers()
				set.healthWG.Wait()
				acknowledgeShutdown()
				return
			}
			if set.healthSnapshotAcceptedHook != nil {
				set.healthSnapshotAcceptedHook(*message.snapshot)
			}
			acceptedSnapshots[message.snapshot.index] = true
			if message.snapshot.event != nil && message.snapshot.event.index < best.index {
				best = *message.snapshot.event
			}
		case <-set.healthStop:
			acknowledgeShutdown()
			return
		case request := <-set.healthShutdownRequests:
			if set.healthShutdownAcceptedHook != nil {
				set.healthShutdownAcceptedHook()
			}
			shutdownAcknowledgement = request.ack
		}
	}
	set.commitHealthError(best.err)
	set.stopHealthWatchers()
	set.healthWG.Wait()
	acknowledgeShutdown()
}

func (set *heldSessionSet) commitHealthError(err error) {
	if err == nil {
		return
	}
	set.healthMu.Lock()
	defer set.healthMu.Unlock()
	if set.healthError == nil {
		set.healthError = err
		set.healthDoneOnce.Do(func() { close(set.healthDone) })
	}
}
