//go:build linux

package process

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSQLiteJournalWatch_CapturesExactIdentityAndCloses(t *testing.T) {
	// Given
	path := writeJournal(t, "dashboard.sqlite-journal")
	watch, err := OpenSQLiteJournalWatch(path)
	requireNoError(t, err)
	t.Cleanup(func() { requireNoError(t, watch.Close()) })

	// When
	identity := watch.Identity()

	// Then
	if identity.MountID == 0 || identity.Inode == 0 || identity.BirthTime.Sec == 0 {
		t.Fatalf("identity = %#v, want complete statx identity", identity)
	}
	if err := watch.Verify(); err != nil {
		t.Fatalf("verify identity: %v", err)
	}
	journalFD, inotifyFD := watch.journalFD, watch.inotifyFD
	requireNoError(t, watch.Close())
	requireNoError(t, watch.Close())
	if _, err := unix.FcntlInt(uintptr(journalFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("journal descriptor remains open: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(inotifyFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("inotify descriptor remains open: %v", err)
	}
}

func TestSQLiteJournalWatch_RejectsReplacementPathDrift(t *testing.T) {
	// Given
	path := writeJournal(t, "dashboard.sqlite-journal")
	watch, err := OpenSQLiteJournalWatch(path)
	requireNoError(t, err)
	t.Cleanup(func() { requireNoError(t, watch.Close()) })
	replacement := filepath.Join(filepath.Dir(path), "replacement")
	requireNoError(t, os.WriteFile(replacement, []byte("replacement"), 0o600))
	requireNoError(t, os.Rename(replacement, path))

	// When
	err = watch.Verify()

	// Then
	if !errors.Is(err, ErrSQLiteJournalIdentityMismatch) {
		t.Fatalf("verify error = %v, want identity mismatch", err)
	}
}

func TestSQLiteJournalWatch_VerifiesIdentityForEveryWindowSample(t *testing.T) {
	// Given
	path := writeJournal(t, "dashboard.sqlite-journal")
	watch, err := OpenSQLiteJournalWatch(path)
	requireNoError(t, err)
	t.Cleanup(func() { requireNoError(t, watch.Close()) })
	verified := 0

	// When
	window, err := SampleWindow(t.Context(), WindowSpec{PID: os.Getpid(), Interval: time.Millisecond, ObserveSample: func(ctx context.Context, sample Sample) error {
		verified++
		return watch.ObserveSample(ctx, sample)
	}})

	// Then
	requireNoError(t, err)
	if len(window.Samples) != 5 || verified != 5 {
		t.Fatalf("samples = %d, verified = %d, want 5", len(window.Samples), verified)
	}
}

func TestSQLiteJournalIdentity_RejectsMissingRequiredStatxMask(t *testing.T) {
	// Given
	stat := unix.Statx_t{Mask: unix.STATX_MNT_ID, Mnt_id: 1, Ino: 2}

	// When / Then
	if _, err := sqliteJournalIdentity(stat); !errors.Is(err, ErrSQLiteJournalUnsupported) {
		t.Fatalf("birth-time error = %v, want unsupported", err)
	}
	stat.Mask = unix.STATX_BTIME
	if _, err := sqliteJournalIdentity(stat); !errors.Is(err, ErrSQLiteJournalUnsupported) {
		t.Fatalf("mount-ID error = %v, want unsupported", err)
	}
}

func TestSQLiteJournalWatch_RejectsInvalidLifecycleEvents(t *testing.T) {
	for name, mask := range map[string]uint32{
		"overflow":      unix.IN_Q_OVERFLOW,
		"move self":     unix.IN_MOVE_SELF,
		"unmount":       unix.IN_UNMOUNT,
		"ignored":       unix.IN_IGNORED,
		"missing close": unix.IN_DELETE_SELF,
	} {
		t.Run(name, func(t *testing.T) {
			// Given
			watch := &SQLiteJournalWatch{}
			// When
			err := watch.observe(mask)

			// Then
			if err == nil {
				t.Fatal("invalid lifecycle event was accepted")
			}
		})
	}
}

func TestSQLiteJournalWatch_RejectsDuplicateTerminalEvent(t *testing.T) {
	// Given
	watch := &SQLiteJournalWatch{}
	requireNoError(t, watch.observe(unix.IN_CLOSE_WRITE))
	requireNoError(t, watch.observe(unix.IN_DELETE_SELF))

	// When
	err := watch.observe(unix.IN_DELETE_SELF)

	// Then
	if !errors.Is(err, ErrSQLiteJournalLifecycle) {
		t.Fatalf("duplicate terminal error = %v, want lifecycle error", err)
	}
}

func TestSQLiteJournalWatch_ReadEventsRejectsTruncatedName(t *testing.T) {
	// Given
	pipe := make([]int, 2)
	requireNoError(t, unix.Pipe(pipe))
	readFD, writeFD := pipe[0], pipe[1]
	t.Cleanup(func() { requireNoError(t, unix.Close(readFD)) })
	t.Cleanup(func() { requireNoError(t, unix.Close(writeFD)) })
	watch := &SQLiteJournalWatch{inotifyFD: readFD}
	buffer := make([]byte, unix.SizeofInotifyEvent)
	binary.NativeEndian.PutUint32(buffer[12:], 1)
	_, err := unix.Write(writeFD, buffer)
	requireNoError(t, err)

	// When
	err = watch.readEvents()

	// Then
	if !errors.Is(err, ErrSQLiteJournalLifecycle) {
		t.Fatalf("truncated event error=%v, want lifecycle error", err)
	}
}

func TestDecodeInotifyEvent_DecodesUnalignedNativeEndianEvent(t *testing.T) {
	// Given
	name := []byte("journal\x00\x00")
	buffer := append([]byte{0xff}, make([]byte, unix.SizeofInotifyEvent+len(name))...)
	eventBytes := buffer[1:]
	binary.NativeEndian.PutUint32(eventBytes, 17)
	binary.NativeEndian.PutUint32(eventBytes[4:], unix.IN_DELETE)
	binary.NativeEndian.PutUint32(eventBytes[12:], uint32(len(name)))
	copy(eventBytes[unix.SizeofInotifyEvent:], name)

	// When
	event, consumed, err := decodeInotifyEvent(eventBytes)

	// Then
	requireNoError(t, err)
	if event.watchDescriptor != 17 || event.mask != unix.IN_DELETE || string(event.name) != "journal" {
		t.Fatalf("event=%+v", event)
	}
	if consumed != len(eventBytes) {
		t.Fatalf("consumed=%d, want %d", consumed, len(eventBytes))
	}
}

func TestDecodeInotifyEvent_RejectsTruncatedName(t *testing.T) {
	// Given
	buffer := make([]byte, unix.SizeofInotifyEvent)
	binary.NativeEndian.PutUint32(buffer[12:], 1)

	// When
	_, _, err := decodeInotifyEvent(buffer)

	// Then
	if err == nil {
		t.Fatal("truncated inotify name was accepted")
	}
}

func TestSQLiteJournalWatch_WaitsForCloseThenDeleteAndCancellation(t *testing.T) {
	// Given
	path := writeJournal(t, "dashboard.sqlite-journal")
	watch, err := OpenSQLiteJournalWatch(path)
	requireNoError(t, err)
	t.Cleanup(func() { requireNoError(t, watch.Close()) })
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- watch.Wait(ctx) }()

	// When
	cancel()
	err = <-result

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want cancellation", err)
	}
	if err := watch.observe(unix.IN_CLOSE_WRITE); err != nil {
		t.Fatalf("close write: %v", err)
	}
	if err := watch.observe(unix.IN_DELETE_SELF); err != nil {
		t.Fatalf("delete self: %v", err)
	}
}

func TestSQLiteJournalWatch_WaitsForExactCloseDeleteLifecycle(t *testing.T) {
	// Given
	path := writeJournal(t, "dashboard.sqlite-journal")
	watch, err := OpenSQLiteJournalWatch(path)
	requireNoError(t, err)
	t.Cleanup(func() { requireNoError(t, watch.Close()) })
	result := make(chan error, 1)
	go func() { result <- watch.Wait(t.Context()) }()
	journal, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	requireNoError(t, err)
	requireNoError(t, journal.Close())
	requireNoError(t, os.Remove(path))

	// When
	err = <-result

	// Then
	requireNoError(t, err)
}

func writeJournal(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	requireNoError(t, os.WriteFile(path, []byte("journal"), 0o600))
	return path
}
