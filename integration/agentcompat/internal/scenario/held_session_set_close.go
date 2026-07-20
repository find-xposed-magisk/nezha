//go:build linux

package scenario

import (
	"context"
	"errors"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

func newHeldSessionSet(plans []StressSessionPlan, state heldSessionSetStateObserver, baseline client.IOStreamState, dependencies HeldSessionSetDependencies, base context.Context) *heldSessionSet {
	return &heldSessionSet{plans: plans, sessions: make([]heldSession, len(plans)), state: state, baseline: baseline, dependencies: dependencies, healthContext: base, healthDone: make(chan struct{}), healthStop: make(chan struct{}), healthShutdown: make(chan struct{}), healthShutdownRequests: make(chan heldHealthShutdownRequest), healthErrors: make([]error, len(plans)), coordinatorDone: make(chan struct{}), closeDone: make(chan struct{})}
}

func (set *heldSessionSet) rollback(ctx context.Context) error {
	ownerContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return set.closeAll(ownerContext)
}

func (set *heldSessionSet) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	set.closeOnce.Do(func() {
		set.beginOwnedClose()
		if set.healthEvents == nil {
			set.markHealthCoordinatorDone()
		}
		go func() {
			ownerContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			ack := make(chan struct{})
			shutdownSent := false
			select {
			case set.healthShutdownRequests <- heldHealthShutdownRequest{ack: ack}:
				shutdownSent = true
			case <-set.coordinatorDone:
			}
			if shutdownSent {
				select {
				case <-ack:
				case <-set.coordinatorDone:
				}
			}
			<-set.coordinatorDone
			set.healthWG.Wait()
			set.closeError = redactHeldSessionSetError(set.closeAll(ownerContext))
			close(set.closeDone)
		}()
	})
	select {
	case <-set.closeDone:
		return set.closeError
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (set *heldSessionSet) closeAll(ctx context.Context) error {
	var joined error
	streamIDs := make([]string, 0, len(set.sessions))
	for index := len(set.sessions) - 1; index >= 0; index-- {
		session := set.sessions[index]
		if session == nil {
			continue
		}
		joined = errors.Join(joined, session.Close(ctx), session.WaitClosed(ctx))
		streamID, present := session.IOStreamID()
		if present && streamID != "" {
			streamIDs = append(streamIDs, streamID)
		}
	}
	if len(streamIDs) > 0 {
		joined = errors.Join(joined, waitHeldSessionSetStreams(ctx, set.state, set.baseline.Count, streamIDs, false, set.dependencies.WaitState))
		_, aggregateErr := set.dependencies.WaitState(ctx, set.state, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(set.baseline.Count)})
		joined = errors.Join(joined, aggregateErr)
	}
	return joined
}
