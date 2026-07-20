//go:build linux

package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

var ErrSQLiteJournalLifecycle = errors.New("invalid sqlite journal lifecycle")

type SQLiteJournalLifecycleError struct{ Event uint32 }

func (err *SQLiteJournalLifecycleError) Error() string { return ErrSQLiteJournalLifecycle.Error() }
func (err *SQLiteJournalLifecycleError) Unwrap() error { return ErrSQLiteJournalLifecycle }

type SQLiteJournalWatch struct {
	path        string
	journalFD   int
	inotifyFD   int
	identity    SQLiteJournalIdentity
	journalWD   int
	directoryWD int
	journalName []byte
	closed      sqliteJournalCloser
	mu          sync.Mutex
	closeSeen   bool
	deleted     bool
}

func OpenSQLiteJournalWatch(path string) (*SQLiteJournalWatch, error) {
	journalFD, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	identity, err := readSQLiteJournalIdentity(journalFD)
	if err != nil {
		_ = unix.Close(journalFD)
		return nil, err
	}
	inotifyFD, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		_ = unix.Close(journalFD)
		return nil, err
	}
	journalWD, err := unix.InotifyAddWatch(inotifyFD, fmt.Sprintf("/proc/self/fd/%d", journalFD), unix.IN_CLOSE_WRITE|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_UNMOUNT)
	if err != nil {
		_ = unix.Close(inotifyFD)
		_ = unix.Close(journalFD)
		return nil, err
	}
	directoryWD, err := unix.InotifyAddWatch(inotifyFD, filepath.Dir(path), unix.IN_DELETE|unix.IN_UNMOUNT)
	if err != nil {
		_ = unix.Close(inotifyFD)
		_ = unix.Close(journalFD)
		return nil, err
	}
	watch := &SQLiteJournalWatch{path: path, journalFD: journalFD, inotifyFD: inotifyFD, identity: identity, journalWD: journalWD, directoryWD: directoryWD, journalName: []byte(filepath.Base(path))}
	if err := watch.Verify(); err != nil {
		_ = watch.Close()
		return nil, err
	}
	return watch, nil
}

func (watch *SQLiteJournalWatch) Identity() SQLiteJournalIdentity { return watch.identity }

func (watch *SQLiteJournalWatch) ObserveSample(context.Context, Sample) error { return watch.Verify() }

func (watch *SQLiteJournalWatch) Verify() error {
	fd, err := unix.Open(watch.path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return &SQLiteJournalIdentityError{Expected: watch.identity}
	}
	defer unix.Close(fd)
	actual, err := readSQLiteJournalIdentity(fd)
	if err != nil {
		return err
	}
	if !watch.identity.equal(actual) {
		return &SQLiteJournalIdentityError{Expected: watch.identity, Actual: actual}
	}
	return nil
}

func (watch *SQLiteJournalWatch) Wait(ctx context.Context) error {
	cancelFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return err
	}
	defer unix.Close(cancelFD)
	stop := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		select {
		case <-ctx.Done():
			_, _ = unix.Write(cancelFD, []byte{1, 0, 0, 0, 0, 0, 0, 0})
		case <-stop:
		}
	}()
	defer func() { close(stop); done.Wait() }()
	for {
		fds := []unix.PollFd{{Fd: int32(watch.inotifyFD), Events: unix.POLLIN}, {Fd: int32(cancelFD), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, -1); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return err
		}
		if fds[1].Revents&unix.POLLIN != 0 {
			return ctx.Err()
		}
		if err := watch.readEvents(); err != nil {
			return err
		}
		watch.mu.Lock()
		completed := watch.deleted
		watch.mu.Unlock()
		if completed {
			return nil
		}
	}
}

func (watch *SQLiteJournalWatch) readEvents() error {
	var buffer [unix.SizeofInotifyEvent * 8]byte
	count, err := unix.Read(watch.inotifyFD, buffer[:])
	if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return nil
	}
	if err != nil {
		return err
	}
	for offset := 0; offset+unix.SizeofInotifyEvent <= count; {
		event := (*unix.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
		next := offset + unix.SizeofInotifyEvent + int(event.Len)
		if next > count {
			return &SQLiteJournalLifecycleError{}
		}
		nameStart := offset + unix.SizeofInotifyEvent
		name := bytes.TrimRight(buffer[nameStart:next], "\x00")
		if err := watch.observeEvent(event.Wd, event.Mask, name); err != nil {
			return err
		}
		offset = next
	}
	return nil
}

func (watch *SQLiteJournalWatch) observeEvent(watchDescriptor int32, mask uint32, name []byte) error {
	if int(watchDescriptor) == watch.directoryWD && mask&unix.IN_DELETE != 0 && bytes.Equal(name, watch.journalName) {
		return watch.observe(unix.IN_DELETE_SELF)
	}
	if int(watchDescriptor) != watch.journalWD {
		return nil
	}
	return watch.observe(mask)
}

func (watch *SQLiteJournalWatch) observe(mask uint32) error {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if mask&(unix.IN_Q_OVERFLOW|unix.IN_MOVE_SELF|unix.IN_UNMOUNT) != 0 || mask&unix.IN_IGNORED != 0 && !watch.deleted {
		return &SQLiteJournalLifecycleError{Event: mask}
	}
	if mask&unix.IN_CLOSE_WRITE != 0 {
		if watch.closeSeen || watch.deleted {
			return &SQLiteJournalLifecycleError{Event: mask}
		}
		watch.closeSeen = true
	}
	if mask&unix.IN_DELETE_SELF != 0 {
		if !watch.closeSeen || watch.deleted {
			return &SQLiteJournalLifecycleError{Event: mask}
		}
		watch.deleted = true
	}
	return nil
}

func (watch *SQLiteJournalWatch) Close() error {
	watch.closed.once.Do(func() {
		watch.closed.err = errors.Join(unix.Close(watch.inotifyFD), unix.Close(watch.journalFD))
	})
	return watch.closed.err
}
