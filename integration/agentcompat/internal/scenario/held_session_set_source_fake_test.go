//go:build linux

package scenario

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

type heldSessionHealthFake struct {
	plan       StressSessionPlan
	done       chan struct{}
	closeError error
}

func (fake *heldSessionHealthFake) Plan() StressSessionPlan          { return fake.plan }
func (fake *heldSessionHealthFake) WaitLive(context.Context) error   { return nil }
func (fake *heldSessionHealthFake) Close(context.Context) error      { return nil }
func (fake *heldSessionHealthFake) WaitClosed(context.Context) error { return nil }
func (fake *heldSessionHealthFake) IOStreamID() (string, bool)       { return "health-fake", true }
func (fake *heldSessionHealthFake) Done() <-chan struct{}            { return fake.done }
func (fake *heldSessionHealthFake) CloseResult() error               { return fake.closeError }

func newHeldSessionHealthFake(t *testing.T, plan StressSessionPlan, closeError error) *heldSessionHealthFake {
	t.Helper()
	return &heldSessionHealthFake{plan: plan, done: make(chan struct{}), closeError: closeError}
}

type heldSessionSetTestSession struct {
	mu               sync.Mutex
	plan             StressSessionPlan
	index            int
	streamID         string
	waitLiveError    error
	closeError       error
	waitClosedError  error
	closeResult      error
	done             chan struct{}
	closed           bool
	closeEvents      chan int
	waitClosedEvents chan int
	closeStarted     chan struct{}
	closeRelease     <-chan struct{}
}

func (session *heldSessionSetTestSession) Plan() StressSessionPlan { return session.plan }
func (session *heldSessionSetTestSession) WaitLive(context.Context) error {
	return session.waitLiveError
}
func (session *heldSessionSetTestSession) IOStreamID() (string, bool) {
	return session.streamID, session.streamID != ""
}
func (session *heldSessionSetTestSession) Done() <-chan struct{} { return session.done }
func (session *heldSessionSetTestSession) CloseResult() error    { return session.closeResult }

func (session *heldSessionSetTestSession) prematureClose() {
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.closed {
		session.closed = true
		close(session.done)
	}
}

func (session *heldSessionSetTestSession) Close(context.Context) error {
	if session.closeStarted != nil {
		close(session.closeStarted)
		session.closeStarted = nil
	}
	if session.closeRelease != nil {
		<-session.closeRelease
	}
	session.mu.Lock()
	if !session.closed {
		session.closed = true
		close(session.done)
	}
	if session.closeEvents != nil {
		session.closeEvents <- session.index
	}
	session.mu.Unlock()
	return session.closeError
}

func (session *heldSessionSetTestSession) WaitClosed(context.Context) error {
	if session.waitClosedEvents != nil {
		session.waitClosedEvents <- session.index
	}
	return session.waitClosedError
}

type heldSessionSetConstructorCoordinator struct {
	mu                 sync.Mutex
	startOnce          sync.Once
	ready              chan int
	started            chan struct{}
	startEvents        chan string
	startSlots         []chan struct{}
	completed          chan heldSessionConstructorCompletion
	acquired           chan int
	sessions           map[int]*heldSessionSetTestSession
	contextSeen        chan context.Context
	blockedIndex       int
	blockedWaiting     chan struct{}
	blockedCanceled    chan struct{}
	errors             map[string]error
	indices            map[string]int
	released           []bool
	lateSuccessIndex   int
	lateSuccessWaiting chan struct{}
	lateSuccessReady   chan struct{}
	lateSuccessRelease chan struct{}
	errorReady         map[int]chan struct{}
}

type heldSessionConstructorCompletion struct {
	index    int
	acquired bool
	err      error
}

func newHeldSessionSetConstructorCoordinator() *heldSessionSetConstructorCoordinator {
	startSlots := make([]chan struct{}, 12)
	for index := range startSlots {
		startSlots[index] = make(chan struct{})
	}
	return &heldSessionSetConstructorCoordinator{ready: make(chan int, 12), started: make(chan struct{}), startEvents: make(chan string, 12), startSlots: startSlots, completed: make(chan heldSessionConstructorCompletion, 12), acquired: make(chan int, 12), sessions: make(map[int]*heldSessionSetTestSession), contextSeen: make(chan context.Context, 12), blockedIndex: -1, errors: make(map[string]error), indices: make(map[string]int), released: make([]bool, 12), lateSuccessIndex: -1, errorReady: make(map[int]chan struct{})}
}

func (coordinator *heldSessionSetConstructorCoordinator) construct(ctx context.Context, plan StressSessionPlan) (heldSession, error) {
	index, exists := coordinator.indices[plan.ID.String()]
	if !exists {
		coordinator.completed <- heldSessionConstructorCompletion{index: -1, err: errors.New("constructor plan is not coordinated")}
		return nil, errors.New("constructor plan is not coordinated")
	}
	coordinator.ready <- index
	<-coordinator.started
	if index == coordinator.blockedIndex {
		coordinator.blockedWaiting <- struct{}{}
		<-ctx.Done()
		coordinator.blockedCanceled <- struct{}{}
		coordinator.completed <- heldSessionConstructorCompletion{index: index, err: ctx.Err()}
		return nil, ctx.Err()
	}
	if coordinator.errors[plan.ID.String()] != nil {
		if ready := coordinator.errorReady[index]; ready != nil {
			<-ready
		}
		coordinator.startEvents <- plan.ID.String()
		err := coordinator.errors[plan.ID.String()]
		coordinator.completed <- heldSessionConstructorCompletion{index: index, err: err}
		return nil, err
	}
	if !coordinator.slotReleased(index) {
		if index == coordinator.lateSuccessIndex {
			coordinator.lateSuccessWaiting <- struct{}{}
			<-coordinator.startSlots[index]
		} else {
			select {
			case <-coordinator.startSlots[index]:
			case <-ctx.Done():
				err := ctx.Err()
				coordinator.startEvents <- plan.ID.String()
				coordinator.completed <- heldSessionConstructorCompletion{index: index, err: err}
				return nil, err
			}
		}
	}
	coordinator.startEvents <- plan.ID.String()
	if err := coordinator.errors[plan.ID.String()]; err != nil {
		coordinator.completed <- heldSessionConstructorCompletion{index: index, err: err}
		return nil, err
	}
	if index == coordinator.lateSuccessIndex {
		coordinator.lateSuccessReady <- struct{}{}
		<-coordinator.lateSuccessRelease
	}
	coordinator.completed <- heldSessionConstructorCompletion{index: index, acquired: true}
	coordinator.acquired <- index
	return coordinator.sessions[index], nil
}

func (coordinator *heldSessionSetConstructorCoordinator) releaseError(index int) {
	close(coordinator.errorReady[index])
}

func (coordinator *heldSessionSetConstructorCoordinator) releaseAll() {
	coordinator.startOnce.Do(func() { close(coordinator.started) })
}
func (coordinator *heldSessionSetConstructorCoordinator) release(index int) {
	coordinator.mu.Lock()
	coordinator.released[index] = true
	coordinator.mu.Unlock()
	close(coordinator.startSlots[index])
}

func (coordinator *heldSessionSetConstructorCoordinator) slotReleased(index int) bool {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.released[index]
}

type heldSessionSetStateFake struct {
	mu                       sync.Mutex
	state                    client.IOStreamState
	baseline                 client.IOStreamState
	snapshotError            error
	present                  []string
	absent                   []string
	counts                   []int
	presentStreamErrors      map[string]error
	absentStreamErrors       map[string]error
	presentAggregateError    error
	absentAggregateError     error
	presentAggregateCalls    int
	absentAggregateCalls     int
	aggregatePredicatesEmpty bool
	waitCalls                chan client.IOStreamStateExpectation
	expectedPresentCount     int
	expectedAbsentCount      int
}

func (fake *heldSessionSetStateFake) IOStreamState(context.Context) (client.IOStreamState, error) {
	if fake.baseline.Count == 0 && fake.state.Count != 0 {
		return fake.state, fake.snapshotError
	}
	return fake.baseline, fake.snapshotError
}

func (fake *heldSessionSetStateFake) wait(ctx context.Context, expectation client.IOStreamStateExpectation) (client.IOStreamState, error) {
	return fake.WaitForIOStreamState(ctx, expectation)
}

func (fake *heldSessionSetStateFake) WaitForIOStreamState(ctx context.Context, expectation client.IOStreamStateExpectation) (client.IOStreamState, error) {
	fake.mu.Lock()
	fake.counts = append(fake.counts, *expectation.ExpectedCount)
	if expectation.PresentStreamID != "" {
		fake.present = append(fake.present, expectation.PresentStreamID)
	}
	if expectation.AbsentStreamID != "" {
		fake.absent = append(fake.absent, expectation.AbsentStreamID)
	}
	isAggregate := expectation.PresentStreamID == "" && expectation.AbsentStreamID == ""
	err := fake.presentAggregateError
	expectedCount := fake.expectedPresentCount
	if expectation.AbsentStreamID != "" {
		expectedCount = fake.expectedAbsentCount
		err = fake.absentStreamErrors[expectation.AbsentStreamID]
	} else if expectation.PresentStreamID != "" {
		err = fake.presentStreamErrors[expectation.PresentStreamID]
	} else if *expectation.ExpectedCount == fake.expectedAbsentCount {
		expectedCount = fake.expectedAbsentCount
		err = fake.absentAggregateError
	}
	if isAggregate {
		fake.aggregatePredicatesEmpty = fake.aggregatePredicatesEmpty || expectation.PresentStreamID == "" && expectation.AbsentStreamID == ""
		if *expectation.ExpectedCount == fake.expectedPresentCount {
			fake.presentAggregateCalls++
		} else if *expectation.ExpectedCount == fake.expectedAbsentCount {
			fake.absentAggregateCalls++
		} else {
			err = errors.Join(err, ErrHeldSessionSetOperation)
		}
	}
	if expectedCount != 0 && !isAggregate && *expectation.ExpectedCount != expectedCount {
		err = errors.Join(err, ErrHeldSessionSetOperation)
	}
	if fake.waitCalls != nil {
		fake.waitCalls <- expectation
	}
	fake.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return client.IOStreamState{}, err
	}
	return fake.baseline, err
}

func newHeldSessionSetTestSession(index int, plan StressSessionPlan, closeEvents, waitClosedEvents chan int) *heldSessionSetTestSession {
	return &heldSessionSetTestSession{plan: plan, index: index, streamID: "stream-" + plan.ID.String(), done: make(chan struct{}), closeEvents: closeEvents, waitClosedEvents: waitClosedEvents}
}
