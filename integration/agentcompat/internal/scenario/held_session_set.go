//go:build linux

package scenario

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/integration/agentcompat/internal/dashboard"
)

type heldSessionSet struct {
	mu                             sync.Mutex
	plans                          []StressSessionPlan
	sessions                       []heldSession
	state                          heldSessionSetStateObserver
	dependencies                   HeldSessionSetDependencies
	baseline                       client.IOStreamState
	healthContext                  context.Context
	healthMu                       sync.Mutex
	healthError                    error
	healthErrors                   []error
	healthDoneOnce                 sync.Once
	healthDone                     chan struct{}
	healthStop                     chan struct{}
	healthStopOnce                 sync.Once
	healthShutdown                 chan struct{}
	healthShutdownOnce             sync.Once
	healthShutdownRequests         chan heldHealthShutdownRequest
	healthWG                       sync.WaitGroup
	healthCoordinatorWG            sync.WaitGroup
	healthCoordinatorDoneOnce      sync.Once
	healthEvents                   chan heldHealthMessage
	healthSnapshots                []chan heldHealthSnapshotRequest
	coordinatorDone                chan struct{}
	healthSnapshotRequestHook      func(int)
	healthSnapshotReplyHook        func(int)
	healthSnapshotOverrideHook     func(int, heldHealthSnapshotRequest) *heldHealthSnapshot
	healthSnapshotSendHook         func(int)
	healthEventHook                func(heldHealthMessage)
	healthClosureObservedHook      func(int)
	healthSnapshotAcceptedHook     func(heldHealthSnapshot)
	healthShutdownAcceptedHook     func()
	healthShutdownAcknowledgedHook func()
	healthWatcherDone              []chan struct{}
	closing                        bool
	closeOnce                      sync.Once
	closeDone                      chan struct{}
	closeError                     error
}

func NewHeldSessionSet(ctx context.Context, input HeldSessionSetInput) (*heldSessionSet, error) {
	if ctx == nil {
		return nil, ErrInvalidHeldSessionSetTopology
	}
	plans, err := validateHeldSessionSetPlans(input.Plan)
	if err != nil {
		return nil, redactHeldSessionSetError(err)
	}
	topology, err := validateHeldSessionSetTopology(input, plans)
	if err != nil {
		return nil, redactHeldSessionSetError(err)
	}
	dependencies := input.Dependencies
	if (dependencies.Terminal == nil) != (dependencies.NAT == nil) || (dependencies.Terminal == nil) != (dependencies.FM == nil) || (dependencies.Terminal == nil) != (dependencies.Snapshot == nil) || (dependencies.Terminal == nil) != (dependencies.WaitState == nil) || (dependencies.Terminal == nil) != (dependencies.InspectAgent == nil) || (dependencies.Terminal == nil) != (dependencies.ObserveState == nil) {
		return nil, redactHeldSessionSetError(ErrInvalidHeldSessionSetTopology)
	}
	if dependencies.Terminal == nil {
		dependencies = defaultHeldSessionSetDependencies()
	}
	baseline, err := dependencies.Snapshot(ctx, topology.stateClient)
	if err != nil {
		return nil, redactHeldSessionSetError(err)
	}
	set := newHeldSessionSet(plans, topology.stateClient, baseline, dependencies, ctx)
	set.healthSnapshotRequestHook = input.testHealthSnapshotRequestHook
	set.healthSnapshotReplyHook = input.testHealthSnapshotReplyHook
	set.healthSnapshotOverrideHook = input.testHealthSnapshotOverrideHook
	set.healthSnapshotSendHook = input.testHealthSnapshotSendHook
	set.healthEventHook = input.testHealthEventHook
	set.healthClosureObservedHook = input.testHealthClosureObservedHook
	set.healthSnapshotAcceptedHook = input.testHealthSnapshotAcceptedHook
	set.healthShutdownAcceptedHook = input.testHealthShutdownAcceptedHook
	set.healthShutdownAcknowledgedHook = input.testHealthShutdownAcknowledgedHook
	if err := set.construct(ctx, topology); err != nil {
		return nil, redactHeldSessionSetError(err)
	}
	if err := set.waitLive(ctx); err != nil {
		return nil, redactHeldSessionSetError(errors.Join(err, set.Close(context.WithoutCancel(ctx))))
	}
	set.startHealthWatchers()
	return set, nil
}

func (set *heldSessionSet) construct(ctx context.Context, topology heldSessionSetTopology) error {
	acquisitionContext, cancelAcquisition := context.WithCancel(ctx)
	defer cancelAcquisition()
	ready := make(chan struct{}, len(set.plans))
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorkers := func() { releaseOnce.Do(func() { close(release) }) }
	group, groupContext := errgroup.WithContext(acquisitionContext)
	var mu sync.Mutex
	var constructionErrors error
	for index, plan := range set.plans {
		index, plan := index, plan
		group.Go(func() error {
			select {
			case ready <- struct{}{}:
			case <-groupContext.Done():
				return groupContext.Err()
			}
			select {
			case <-release:
			case <-groupContext.Done():
				return groupContext.Err()
			}
			topologyAgent := topology.agents[plan.Agent.Int()]
			// Acquisition cancellation unblocks siblings; successful sessions retain the outer lifetime context.
			session, err := constructHeldSession(groupContext, ctx, topology.dashboard, topologyAgent, plan, set.dependencies)
			if err != nil {
				mu.Lock()
				constructionErrors = errors.Join(constructionErrors, err)
				mu.Unlock()
				return err
			}
			mu.Lock()
			set.sessions[index] = session
			mu.Unlock()
			return nil
		})
	}
	for range set.plans {
		select {
		case <-ready:
		case <-groupContext.Done():
			releaseWorkers()
			groupError := group.Wait()
			return errors.Join(groupContext.Err(), groupError, constructionErrors, set.rollback(ctx))
		}
	}
	releaseWorkers()
	groupError := group.Wait()
	if groupError != nil {
		return errors.Join(groupError, constructionErrors, set.rollback(ctx))
	}
	return nil
}

func constructHeldSession(ctx, lifetimeContext context.Context, dashboardInstance *dashboard.Dashboard, topology HeldSessionAgent, plan StressSessionPlan, dependencies HeldSessionSetDependencies) (heldSession, error) {
	switch plan.Kind {
	case StressSessionTerminal:
		return dependencies.Terminal(ctx, heldTerminalInput{Dashboard: dashboardInstance, PATClient: topology.PATClient, Agent: topology.Agent, Readiness: topology.Readiness, Plan: plan, LifetimeContext: lifetimeContext})
	case StressSessionNAT:
		return dependencies.NAT(ctx, heldNATInput{Dashboard: dashboardInstance, PATClient: topology.PATClient, Agent: topology.Agent, Readiness: topology.Readiness, Plan: plan, LifetimeContext: lifetimeContext})
	case StressSessionFM:
		return dependencies.FM(ctx, heldLegacyFMInput{Dashboard: dashboardInstance, PATClient: topology.PATClient, Agent: topology.Agent, Readiness: topology.Readiness, Plan: plan, LifetimeContext: lifetimeContext})
	default:
		return nil, fmt.Errorf("unsupported held session kind %q: %w", plan.Kind, ErrInvalidHeldSessionSetPlan)
	}
}

func (set *heldSessionSet) waitLive(ctx context.Context) error {
	group, groupContext := errgroup.WithContext(ctx)
	for index, session := range set.sessions {
		index, session := index, session
		group.Go(func() error {
			if session == nil {
				return fmt.Errorf("session %d was not constructed", index)
			}
			if err := session.WaitLive(groupContext); err != nil {
				return err
			}
			select {
			case <-session.Done():
				return errors.Join(ErrHeldSessionPrematureClose, session.CloseResult())
			default:
			}
			if !sessionMatchesPlan(session, set.plans[index]) {
				return fmt.Errorf("session %d does not match its plan", index)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	for _, session := range set.sessions {
		if session == nil {
			return errors.New("held session set has an unconstructed session")
		}
	}
	streamIDs, err := set.streamIDs()
	if err != nil {
		return err
	}
	streamErr := waitHeldSessionSetStreams(ctx, set.state, set.baseline.Count+len(streamIDs), streamIDs, true, set.dependencies.WaitState)
	_, aggregateErr := set.dependencies.WaitState(ctx, set.state, client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(set.baseline.Count + len(streamIDs))})
	return errors.Join(streamErr, aggregateErr)
}

func (set *heldSessionSet) streamIDs() ([]string, error) {
	ids := make([]string, len(set.sessions))
	seen := make(map[string]struct{}, len(ids))
	for index, session := range set.sessions {
		streamID, present := session.IOStreamID()
		if !present || streamID == "" {
			return nil, errors.New("held session stream ID is empty")
		}
		if _, exists := seen[streamID]; exists {
			return nil, fmt.Errorf("duplicate held session stream ID: %w", ErrInvalidHeldSessionSetPlan)
		}
		seen[streamID] = struct{}{}
		ids[index] = streamID
	}
	return ids, nil
}

func sessionMatchesPlan(session heldSession, plan StressSessionPlan) bool {
	return session.Plan() == plan
}

func waitHeldSessionSetStreams(ctx context.Context, state heldSessionSetStateObserver, expectedCount int, streamIDs []string, present bool, waitState func(context.Context, heldSessionSetStateObserver, client.IOStreamStateExpectation) (client.IOStreamState, error)) error {
	group, groupContext := errgroup.WithContext(ctx)
	var mu sync.Mutex
	var joined error
	for _, streamID := range streamIDs {
		streamID := streamID
		group.Go(func() error {
			expectation := client.IOStreamStateExpectation{ExpectedCount: client.ExpectedIOStreamCount(expectedCount)}
			if present {
				expectation.PresentStreamID = streamID
			} else {
				expectation.AbsentStreamID = streamID
			}
			_, err := waitState(groupContext, state, expectation)
			if err != nil {
				mu.Lock()
				joined = errors.Join(joined, err)
				mu.Unlock()
			}
			return nil
		})
	}
	return errors.Join(group.Wait(), joined)
}
