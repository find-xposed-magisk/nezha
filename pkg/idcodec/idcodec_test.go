package idcodec

import (
	"strings"
	"sync"
	"testing"
)

const testMasterKey = "this-is-a-32-byte-master-key-ok!"

func resetEncoder(t *testing.T) {
	t.Helper()
	mu.Lock()
	encoder = nil
	mu.Unlock()
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	resetEncoder(t)
	if err := Init([]byte(testMasterKey)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cases := []uint64{1, 2, 42, 1_000_000, 1<<63 - 1}
	for _, id := range cases {
		code, err := Encode(id)
		if err != nil {
			t.Fatalf("Encode(%d): %v", id, err)
		}
		if len(code) < minLength {
			t.Fatalf("code %q shorter than min %d", code, minLength)
		}
		got, err := Decode(code)
		if err != nil {
			t.Fatalf("Decode(%q): %v", code, err)
		}
		if got != id {
			t.Fatalf("round-trip mismatch: got %d, want %d", got, id)
		}
	}
}

func TestEncodeBeforeInit(t *testing.T) {
	resetEncoder(t)
	if _, err := Encode(1); err != ErrNotInitialized {
		t.Fatalf("Encode without Init: want ErrNotInitialized, got %v", err)
	}
	if _, err := Decode("abcdefgh"); err != ErrNotInitialized {
		t.Fatalf("Decode without Init: want ErrNotInitialized, got %v", err)
	}
}

func TestInitRejectsShortMasterKey(t *testing.T) {
	resetEncoder(t)
	if err := Init([]byte("too-short")); err != ErrMasterKeyShort {
		t.Fatalf("Init short master key: want ErrMasterKeyShort, got %v", err)
	}
}

func TestDecodeInvalidInputs(t *testing.T) {
	resetEncoder(t)
	if err := Init([]byte(testMasterKey)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, code := range []string{"", "@@@@", strings.Repeat("!", 16)} {
		if _, err := Decode(code); err == nil {
			t.Fatalf("Decode(%q) must fail", code)
		}
	}
}

func TestAlphabetChangesWithMasterKey(t *testing.T) {
	resetEncoder(t)
	if err := Init([]byte(testMasterKey)); err != nil {
		t.Fatalf("Init A: %v", err)
	}
	codeA, err := Encode(42)
	if err != nil {
		t.Fatalf("Encode A: %v", err)
	}

	resetEncoder(t)
	if err := Init([]byte(testMasterKey + "rotated-suffix-makes-key-longer!")); err != nil {
		t.Fatalf("Init B: %v", err)
	}
	codeB, err := Encode(42)
	if err != nil {
		t.Fatalf("Encode B: %v", err)
	}
	if codeA == codeB {
		t.Fatalf("rotating master key must change hashid encoding for the same id; both produced %q", codeA)
	}

	if _, err := Decode(codeA); err == nil {
		t.Fatalf("after rotation, old hashid %q must not decode under new key", codeA)
	}
}

func TestConcurrentEncodeDecodeIsSafe(t *testing.T) {
	resetEncoder(t)
	if err := Init([]byte(testMasterKey)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			for j := uint64(0); j < 1000; j++ {
				id := seed*1000 + j
				code, err := Encode(id)
				if err != nil {
					t.Errorf("Encode(%d): %v", id, err)
					return
				}
				got, err := Decode(code)
				if err != nil {
					t.Errorf("Decode(%q): %v", code, err)
					return
				}
				if got != id {
					t.Errorf("round-trip: got %d, want %d", got, id)
					return
				}
			}
		}(uint64(i))
	}
	wg.Wait()
}
