//go:build agentcompat && linux

package singleton

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrSQLiteHoldSessionActive          = errors.New("sqlite hold session already active")
	ErrSQLiteHoldAmbiguousCandidate     = errors.New("sqlite hold has multiple matching candidates")
	ErrSQLiteHoldNotSelected            = errors.New("sqlite hold transaction is not selected")
	ErrSQLiteHoldFinalizationStarted    = errors.New("sqlite hold finalization already started")
	ErrSQLiteHoldFinalizationNotStarted = errors.New("sqlite hold finalization not started")
	ErrSQLiteHoldStaleSession           = errors.New("sqlite hold session is stale")
	ErrSQLiteHoldAborted                = errors.New("sqlite hold session aborted")
	ErrSQLiteHoldTransactionActive      = errors.New("sqlite hold transaction already active")
	ErrSQLiteHoldAtomicWriteRecorded    = errors.New("sqlite hold atomic write already recorded")
	ErrSQLiteHoldEvidenceFrozen         = errors.New("sqlite hold selected evidence is frozen")
	ErrSQLiteHoldInvalidWaitTarget      = errors.New("sqlite hold wait target is invalid")
)

type SQLiteConnectionIdentity uint64
type SQLiteTransactionIdentity uint64

type SQLiteJournalIdentity struct {
	MountID          uint64
	DeviceMajor      uint32
	DeviceMinor      uint32
	Inode            uint64
	BirthSeconds     int64
	BirthNanoseconds uint32
}

type SQLiteOperation string

const (
	SQLiteOperationInsert SQLiteOperation = "insert"
	SQLiteOperationUpdate SQLiteOperation = "update"
	SQLiteOperationDelete SQLiteOperation = "delete"
)

type SQLiteTransaction struct {
	Connection SQLiteConnectionIdentity
	Identity   SQLiteTransactionIdentity
}

type SQLiteExecutionOrigin struct {
	Operation       SQLiteOperation
	Table           string
	StackHash       uint64
	FirstNezhaFrame string
}

type SQLiteUpdateObservation struct {
	Operation SQLiteOperation
	Table     string
	Journal   SQLiteJournalIdentity
}

type SQLiteWriteObservation struct {
	Origin SQLiteExecutionOrigin
	Update SQLiteUpdateObservation
}

type SQLiteHoldSelectionMode uint8

const (
	SQLiteHoldSelectionModeKnownJournal SQLiteHoldSelectionMode = iota + 1
	SQLiteHoldSelectionModeNextWriter
)

type SQLiteHoldSession struct{ identity uint64 }

func (session SQLiteHoldSession) ID() uint64 { return session.identity }

type SQLiteHoldSnapshot struct {
	SessionID       uint64
	Mode            SQLiteHoldSelectionMode
	Selected        bool
	Transaction     SQLiteTransaction
	Operation       SQLiteOperation
	Table           string
	StackHash       uint64
	FirstNezhaFrame string
	Journal         SQLiteJournalIdentity
	Finalizing      bool
	Released        bool
	Aborted         bool
}

type SQLiteHoldFinalization struct {
	done chan struct{}
	err  error
}

type SQLiteHoldWaitTarget uint8

const (
	SQLiteHoldWaitSelected SQLiteHoldWaitTarget = iota + 1
	SQLiteHoldWaitFinalizing
)

func (finalization *SQLiteHoldFinalization) Wait() error {
	<-finalization.done
	return finalization.err
}

func (finalization *SQLiteHoldFinalization) Released() bool {
	select {
	case <-finalization.done:
		return finalization.err == nil
	default:
		return false
	}
}

type sqliteHeldTransaction struct {
	transaction SQLiteTransaction
	origin      SQLiteExecutionOrigin
	update      SQLiteUpdateObservation
	hasUpdate   bool
	write       SQLiteWriteObservation
	hasWrite    bool
	finalizing  bool
}

type sqliteHoldSessionState struct {
	id           SQLiteHoldSession
	mode         SQLiteHoldSelectionMode
	journal      SQLiteJournalIdentity
	selected     SQLiteTransaction
	hasSelected  bool
	released     bool
	finalization *SQLiteHoldFinalization
	notify       chan struct{}
}

type sqliteHoldTerminalState struct {
	id          SQLiteHoldSession
	selected    SQLiteTransaction
	hasSelected bool
	released    bool
	cause       error
}

// SQLiteHoldTracker linearizes state transitions; finalization waits only after releasing mu.
type SQLiteHoldTracker struct {
	mu                   sync.Mutex
	attributionEnabled   *atomic.Bool
	nextSession          uint64
	transactions         map[SQLiteTransaction]*sqliteHeldTransaction
	session              *sqliteHoldSessionState
	terminal             *sqliteHoldTerminalState
	causes               map[SQLiteTransaction]error
	terminalArbitrations map[SQLiteTransaction]struct{}
}

func NewSQLiteHoldTracker() *SQLiteHoldTracker {
	return &SQLiteHoldTracker{transactions: make(map[SQLiteTransaction]*sqliteHeldTransaction), causes: make(map[SQLiteTransaction]error), terminalArbitrations: make(map[SQLiteTransaction]struct{})}
}

func newSQLiteAttributionHoldTracker(enabled *atomic.Bool) *SQLiteHoldTracker {
	tracker := NewSQLiteHoldTracker()
	tracker.attributionEnabled = enabled
	return tracker
}
