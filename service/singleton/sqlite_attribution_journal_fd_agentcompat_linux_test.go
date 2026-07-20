//go:build agentcompat && linux

package singleton

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSQLiteAttributionDerivesJournalIdentityFromOpenedDescriptor(t *testing.T) {
	// Given
	journalPath := filepath.Join(t.TempDir(), "dashboard.sqlite-journal")
	descriptor, err := unix.Open(journalPath, unix.O_CREAT|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Close(descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor, err = unix.Open(journalPath, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := unix.Close(descriptor); closeErr != nil {
			t.Error(closeErr)
		}
	})

	// When
	identity, err := sqliteAttributionJournalIdentityFromDescriptor(descriptor)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if identity.MountID == 0 || identity.Inode == 0 || identity.BirthSeconds == 0 {
		t.Fatal("descriptor-derived journal identity is incomplete")
	}
}
