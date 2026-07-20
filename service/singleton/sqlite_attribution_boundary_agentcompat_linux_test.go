//go:build agentcompat && linux

package singleton

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteAttributionErrorsDoNotExposeDatabasePath(t *testing.T) {
	// Given
	databasePath := filepath.Join(t.TempDir(), "private-dashboard.sqlite")
	unsupportedDSN := "file:" + databasePath + "?mode=memory"
	missingJournal := filepath.Join(t.TempDir(), "missing-journal")

	// When
	memoryDatabase, dsnErr := openSQLiteAttributionTestDB(unsupportedDSN)
	if dsnErr == nil {
		dsnErr = memoryDatabase.Ping()
	}
	if memoryDatabase != nil {
		t.Cleanup(func() {
			if closeErr := memoryDatabase.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
	}
	_, journalErr := sqliteAttributionOpenJournalDescriptor(missingJournal)

	// Then
	if !errors.Is(dsnErr, ErrSQLiteAttributionUnsupportedDSN) {
		t.Fatal("file URI error does not wrap the typed unsupported DSN error")
	}
	if !errors.Is(journalErr, ErrSQLiteAttributionJournalIdentity) {
		t.Fatal("journal error does not wrap the typed journal identity error")
	}
	if strings.Contains(dsnErr.Error(), databasePath) || strings.Contains(dsnErr.Error(), unsupportedDSN) {
		t.Fatal("unsupported DSN error exposes its database path")
	}
	if strings.Contains(journalErr.Error(), missingJournal) {
		t.Fatal("journal identity error exposes its database path")
	}
}

func TestSQLiteAttributionAcceptsOnDiskFileURIAndRejectsInMemoryDSN(t *testing.T) {
	// Given
	resetSQLiteAttributionForTest()
	databasePath := filepath.Join(t.TempDir(), "dashboard.sqlite")

	// When
	fileDatabase, fileErr := openSQLiteAttributionTestDB("file:" + databasePath)
	if fileDatabase != nil {
		t.Cleanup(func() {
			if closeErr := fileDatabase.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
	}
	if fileErr == nil {
		fileErr = fileDatabase.Ping()
	}
	memoryDatabase, memoryErr := openSQLiteAttributionTestDB(":memory:")
	if memoryDatabase != nil {
		t.Cleanup(func() {
			if closeErr := memoryDatabase.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
	}
	if memoryErr == nil {
		memoryErr = memoryDatabase.Ping()
	}

	// Then
	if fileErr != nil {
		t.Fatal("file URI for an on-disk database was rejected")
	}
	if !errors.Is(memoryErr, ErrSQLiteAttributionUnsupportedDSN) {
		t.Fatal("in-memory DSN does not return the typed unsupported DSN error")
	}
}
