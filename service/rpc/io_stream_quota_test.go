package rpc

import (
	"errors"
	"fmt"
	"testing"
)

func TestCreateStreamExactUserBoundary(t *testing.T) {
	h := NewNezhaHandler()
	for i := 0; i < maxStreamsPerUser; i++ {
		if err := h.CreateStream(fmt.Sprintf("quota-user-%d", i), 1, uint64(i+1)); err != nil {
			t.Fatalf("20th user stream must succeed: %v", err)
		}
	}
	if err := h.CreateStream("quota-user-21", 1, 100); !errors.Is(err, ErrTooManyStreamsForUser) {
		t.Fatalf("21st user stream must be rejected: %v", err)
	}
}

func TestCreateStreamNormalUserEverydayUseSucceeds(t *testing.T) {
	h := NewNezhaHandler()
	if err := h.CreateStream("term", 7, 1); err != nil {
		t.Fatal(err)
	}
	if err := h.CreateStream("fm", 7, 1); err != nil {
		t.Fatal(err)
	}
}

func TestCreateStreamNormalUsersAreIndependent(t *testing.T) {
	h := NewNezhaHandler()
	for userID := uint64(1); userID <= 5; userID++ {
		for i := 0; i < maxStreamsPerUser; i++ {
			if err := h.CreateStream(fmt.Sprintf("independent-%d-%d", userID, i), userID, 100+userID); err != nil {
				t.Fatalf("user %d stream %d: %v", userID, i, err)
			}
		}
	}
}

func TestCreateStreamExemptsInternalStreamsFromPerUserCap(t *testing.T) {
	h := NewNezhaHandler()
	for i := 0; i < maxStreamsPerUser*3; i++ {
		if err := h.CreateStream(fmt.Sprintf("internal-user-%d", i), 0, uint64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateStreamInternalStreamsStillCountTowardPerServerCap(t *testing.T) {
	h := NewNezhaHandler()
	for i := 0; i < maxStreamsPerServer; i++ {
		if err := h.CreateStream(fmt.Sprintf("internal-server-%d", i), 0, 9); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.CreateStream("internal-server-over", 0, 9); !errors.Is(err, ErrTooManyStreamsForServer) {
		t.Fatal(err)
	}
}

func TestCreateStreamExactServerBoundary(t *testing.T) {
	h := NewNezhaHandler()
	for i := 0; i < maxStreamsPerServer; i++ {
		if err := h.CreateStream(fmt.Sprintf("quota-server-%d", i), uint64(i+1), 2); err != nil {
			t.Fatalf("40th server stream must succeed: %v", err)
		}
	}
	if err := h.CreateStream("quota-server-41", 100, 2); !errors.Is(err, ErrTooManyStreamsForServer) {
		t.Fatalf("41st server stream must be rejected: %v", err)
	}
}

func TestCreateStreamReleasesUserAndServerSlots(t *testing.T) {
	h := NewNezhaHandler()
	for i := 0; i < maxStreamsPerUser; i++ {
		if err := h.CreateStream(fmt.Sprintf("reuse-user-%d", i), 1, uint64(i+10)); err != nil {
			t.Fatalf("user setup stream %d failed: %v", i, err)
		}
	}
	if !errors.Is(h.CreateStream("reuse-user-over", 1, 100), ErrTooManyStreamsForUser) {
		t.Fatal("user cap was not enforced")
	}
	if err := h.CloseStream("reuse-user-0"); err != nil {
		t.Fatal(err)
	}
	if err := h.CreateStream("reuse-user-new", 1, 101); err != nil {
		t.Fatalf("closed user slot must be reusable: %v", err)
	}

	for i := 0; i < maxStreamsPerServer; i++ {
		if err := h.CreateStream(fmt.Sprintf("reuse-server-%d", i), uint64(i+2), 2); err != nil {
			t.Fatalf("server setup stream %d failed: %v", i, err)
		}
	}
	if !errors.Is(h.CreateStream("reuse-server-over", 100, 2), ErrTooManyStreamsForServer) {
		t.Fatal("server cap was not enforced")
	}
	if err := h.CloseStream("reuse-server-0"); err != nil {
		t.Fatal(err)
	}
	if err := h.CreateStream("reuse-server-new", 100, 2); err != nil {
		t.Fatalf("closed server slot must be reusable: %v", err)
	}
}
