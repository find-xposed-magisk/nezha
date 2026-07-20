//go:build linux

package process

import (
	"errors"
	"sync"

	"golang.org/x/sys/unix"
)

var (
	ErrSQLiteJournalUnsupported      = errors.New("sqlite journal identity unsupported")
	ErrSQLiteJournalIdentityMismatch = errors.New("sqlite journal identity mismatch")
)

type SQLiteJournalUnsupportedError struct{ Missing uint32 }

func (err *SQLiteJournalUnsupportedError) Error() string { return ErrSQLiteJournalUnsupported.Error() }
func (err *SQLiteJournalUnsupportedError) Unwrap() error { return ErrSQLiteJournalUnsupported }

type SQLiteJournalIdentity struct {
	MountID     uint64
	DeviceMajor uint32
	DeviceMinor uint32
	Inode       uint64
	BirthTime   unix.StatxTimestamp
}

func (identity SQLiteJournalIdentity) equal(other SQLiteJournalIdentity) bool {
	return identity == other
}

func sqliteJournalIdentity(stat unix.Statx_t) (SQLiteJournalIdentity, error) {
	required := uint32(unix.STATX_MNT_ID | unix.STATX_BTIME)
	if stat.Mask&required != required {
		return SQLiteJournalIdentity{}, &SQLiteJournalUnsupportedError{Missing: required &^ stat.Mask}
	}
	return SQLiteJournalIdentity{
		MountID:     stat.Mnt_id,
		DeviceMajor: stat.Dev_major,
		DeviceMinor: stat.Dev_minor,
		Inode:       stat.Ino,
		BirthTime:   stat.Btime,
	}, nil
}

func readSQLiteJournalIdentity(fd int) (SQLiteJournalIdentity, error) {
	var stat unix.Statx_t
	err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_BASIC_STATS|unix.STATX_MNT_ID|unix.STATX_BTIME, &stat)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) {
			return SQLiteJournalIdentity{}, &SQLiteJournalUnsupportedError{Missing: unix.STATX_MNT_ID | unix.STATX_BTIME}
		}
		return SQLiteJournalIdentity{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return SQLiteJournalIdentity{}, &SQLiteJournalUnsupportedError{}
	}
	return sqliteJournalIdentity(stat)
}

type SQLiteJournalIdentityError struct {
	Expected SQLiteJournalIdentity
	Actual   SQLiteJournalIdentity
}

func (err *SQLiteJournalIdentityError) Error() string {
	return ErrSQLiteJournalIdentityMismatch.Error()
}
func (err *SQLiteJournalIdentityError) Unwrap() error { return ErrSQLiteJournalIdentityMismatch }

type sqliteJournalCloser struct {
	once sync.Once
	err  error
}
