//go:build agentcompat && linux

package singleton

import (
	"errors"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

func (connection *sqliteAttributionConnection) beforeWrite(classification sqliteAttributionClassification) error {
	if !sqliteAttributionEnabled.Load() || classification.readonly {
		return nil
	}
	if !classification.valid() {
		return &SQLiteAttributionError{Cause: ErrSQLiteAttributionUnsupportedWrite}
	}
	connection.lifecycleMu.Lock()
	state := connection.transaction
	if state == nil {
		connection.lifecycleMu.Unlock()
		return &SQLiteAttributionError{Cause: ErrSQLiteAttributionUnboundWrite}
	}
	poison := state.poison
	connection.lifecycleMu.Unlock()
	if poison != nil {
		return poison
	}
	connection.execution = &sqliteAttributionExecution{classification: classification, origin: sqliteAttributionOrigin()}
	return nil
}

func (connection *sqliteAttributionConnection) discardExecution() { connection.execution = nil }

func (connection *sqliteAttributionConnection) publishExecution() error {
	execution := connection.execution
	connection.execution = nil
	if execution == nil {
		return nil
	}
	connection.lifecycleMu.Lock()
	state := connection.transaction
	if state == nil {
		connection.lifecycleMu.Unlock()
		return &SQLiteAttributionError{Cause: ErrSQLiteAttributionUnboundWrite}
	}
	if execution.hook.mismatch || !execution.hook.seen {
		err := &SQLiteAttributionError{Cause: ErrSQLiteAttributionUnsupportedWrite}
		state.poison = err
		connection.lifecycleMu.Unlock()
		return err
	}
	if state.journalFD < 0 {
		descriptor, err := sqliteAttributionOpenJournalDescriptor(connection.journal)
		if err != nil {
			state.poison = err
			connection.lifecycleMu.Unlock()
			return err
		}
		state.journalFD = descriptor
	}
	journal, err := sqliteAttributionJournalIdentityFromDescriptor(state.journalFD)
	if err != nil {
		state.poison = err
		connection.lifecycleMu.Unlock()
		return err
	}
	transaction := state.transaction
	tracker := state.tracker
	connection.lifecycleMu.Unlock()
	origin := execution.origin
	origin.Operation = execution.classification.operation
	origin.Table = execution.classification.table
	err = tracker.RecordSQLiteWrite(transaction, SQLiteWriteObservation{
		Origin: origin,
		Update: SQLiteUpdateObservation{Operation: execution.classification.operation, Table: execution.classification.table, Journal: journal},
	})
	if err != nil {
		return connection.poison(err)
	}
	return nil
}

func (connection *sqliteAttributionConnection) poison(err error) error {
	connection.lifecycleMu.Lock()
	state := connection.transaction
	if state != nil {
		state.poison = err
	}
	connection.lifecycleMu.Unlock()
	return err
}

// The adapter owns the explicit main transaction and DELETE journal; path-only stat is racy, so retain this O_PATH descriptor through finalization.
func sqliteAttributionOpenJournalDescriptor(path string) (int, error) {
	descriptor, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, &SQLiteAttributionError{Cause: errors.Join(ErrSQLiteAttributionJournalIdentity, err)}
	}
	return descriptor, nil
}

func sqliteAttributionCloseJournalDescriptor(descriptor int) error {
	if descriptor < 0 {
		return nil
	}
	if err := unix.Close(descriptor); err != nil {
		return &SQLiteAttributionError{Cause: errors.Join(ErrSQLiteAttributionJournalIdentity, err)}
	}
	return nil
}

func sqliteAttributionJournalIdentityFromDescriptor(descriptor int) (SQLiteJournalIdentity, error) {
	var status unix.Statx_t
	mask := uint32(unix.STATX_BASIC_STATS | unix.STATX_BTIME | unix.STATX_MNT_ID)
	if err := unix.Statx(descriptor, "", unix.AT_EMPTY_PATH|unix.AT_STATX_SYNC_AS_STAT, int(mask), &status); err != nil {
		return SQLiteJournalIdentity{}, &SQLiteAttributionError{Cause: errors.Join(ErrSQLiteAttributionJournalIdentity, err)}
	}
	required := uint32(unix.STATX_MNT_ID | unix.STATX_BTIME)
	if status.Mask&required != required {
		return SQLiteJournalIdentity{}, &SQLiteAttributionError{Cause: ErrSQLiteAttributionJournalIdentity}
	}
	return SQLiteJournalIdentity{MountID: status.Mnt_id, DeviceMajor: status.Dev_major, DeviceMinor: status.Dev_minor, Inode: status.Ino, BirthSeconds: status.Btime.Sec, BirthNanoseconds: status.Btime.Nsec}, nil
}

func sqliteAttributionOrigin() SQLiteExecutionOrigin {
	programCounters := make([]uintptr, 16)
	count := runtime.Callers(3, programCounters)
	programCounters = programCounters[:count]
	frames := runtime.CallersFrames(programCounters)
	frame, more := frames.Next()
	for more && !strings.Contains(frame.Function, "github.com/nezhahq/nezha/") {
		frame, more = frames.Next()
	}
	return SQLiteExecutionOrigin{StackHash: sqliteAttributionStackHash(programCounters), FirstNezhaFrame: frame.Function}
}

func sqliteAttributionStackHash(programCounters []uintptr) uint64 {
	const offsetBasis uint64 = 14695981039346656037
	const prime uint64 = 1099511628211
	hash := offsetBasis
	for _, programCounter := range programCounters {
		hash ^= uint64(programCounter)
		hash *= prime
	}
	return hash
}
