package rpc

import (
	"io"
	"reflect"
	"testing"
	"time"
)

func TestIOStream(t *testing.T) {
	handler := NewNezhaHandler()

	const testStreamID = "ffffffff-ffff-ffff-ffff-ffffffffffff"

	handler.CreateStream(testStreamID, 0)
	userIo, agentIo := newPipeReadWriter(), newPipeReadWriter()
	defer func() {
		userIo.Close()
		agentIo.Close()
	}()

	handler.AgentConnected(testStreamID, agentIo)
	handler.UserConnected(testStreamID, userIo)

	go handler.StartStream(testStreamID, time.Second*10)

	cases := [][]byte{
		{0, 9, 1, 3, 2, 9, 1, 4, 8},
		{3, 1, 3, 5, 2, 9, 5, 13, 53, 23},
		make([]byte, 1024),
		make([]byte, 1024*1024),
	}

	t.Run("WriteUserIO", func(t *testing.T) {
		for i, c := range cases {
			_, err := userIo.Write(c)
			if err != nil {
				t.Fatalf("write to userIo failed at case %d: %v", i, err)
			}

			b := make([]byte, len(c))
			n, err := agentIo.Read(b)
			if err != nil {
				t.Fatalf("read agentIo failed at case %d: %v", i, err)
			}

			if !reflect.DeepEqual(c, b[:n]) {
				t.Fatalf("expected %v, but got %v", c, b[:n])
			}
		}
	})

	t.Run("WriteAgentIO", func(t *testing.T) {
		for i, c := range cases {
			_, err := agentIo.Write(c)
			if err != nil {
				t.Fatalf("write to agentIo failed at case %d: %v", i, err)
			}

			b := make([]byte, len(c))
			n, err := userIo.Read(b)
			if err != nil {
				t.Fatalf("read userIo failed at case %d: %v", i, err)
			}

			if !reflect.DeepEqual(c, b[:n]) {
				t.Fatalf("Expected %v, but got %v", c, b[:n])
			}
		}
	})

	t.Run("WriteUserIOReadTwice", func(t *testing.T) {
		data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
		_, err := agentIo.Write(data)
		if err != nil {
			t.Fatalf("write to agentIo failed: %v", err)
		}

		b := make([]byte, len(data)/2)
		n, err := userIo.Read(b)
		if err != nil {
			t.Fatalf("read userIo failed: %v", err)
		}

		b2 := make([]byte, len(data)-n)
		_, err = userIo.Read(b2)
		if err != nil {
			t.Fatalf("read userIo failed: %v", err)
		}

		if !reflect.DeepEqual(data[:len(data)/2], b) {
			t.Fatalf("expected %v, but got %v", data[:len(data)/2], b)
		}

		if !reflect.DeepEqual(data[len(data)/2:], b2) {
			t.Fatalf("expected %v, but got %v", data[len(data)/2:], b2)
		}
	})
}

func newPipeReadWriter() io.ReadWriteCloser {
	r, w := io.Pipe()
	return struct {
		io.Reader
		io.WriteCloser
	}{r, w}
}

func TestStreamOwnershipReturnsCreatorUserID(t *testing.T) {
	h := NewNezhaHandler()
	h.CreateStream("alice-stream", 100)

	creator, found := h.StreamOwnership("alice-stream")
	if !found {
		t.Fatalf("expected stream to be found after CreateStream")
	}
	if creator != 100 {
		t.Fatalf("expected creator user ID 100, got %d", creator)
	}
}

func TestStreamOwnershipReturnsNotFoundForUnknownID(t *testing.T) {
	h := NewNezhaHandler()
	if _, found := h.StreamOwnership("nonexistent"); found {
		t.Fatalf("expected unknown stream id to report not-found")
	}
}

func TestStreamOwnershipPreservesPerStreamCreator(t *testing.T) {
	h := NewNezhaHandler()
	h.CreateStream("alice-stream", 100)
	h.CreateStream("bob-stream", 200)

	aliceCreator, _ := h.StreamOwnership("alice-stream")
	bobCreator, _ := h.StreamOwnership("bob-stream")
	if aliceCreator != 100 || bobCreator != 200 {
		t.Fatalf("expected per-stream creator IDs alice=100 bob=200, got alice=%d bob=%d",
			aliceCreator, bobCreator)
	}
}

func TestIsStreamAuthorizedForUserAllowsCreator(t *testing.T) {
	h := NewNezhaHandler()
	h.CreateStream("alice-stream", 100)

	if !h.IsStreamAuthorizedForUser("alice-stream", 100, false) {
		t.Fatalf("creator must be authorized to attach to their own stream")
	}
}

func TestIsStreamAuthorizedForUserDeniesForeignMember(t *testing.T) {
	h := NewNezhaHandler()
	h.CreateStream("alice-stream", 100)

	if h.IsStreamAuthorizedForUser("alice-stream", 200, false) {
		t.Fatalf("foreign member must not be authorized — session hijack would be possible")
	}
}

func TestIsStreamAuthorizedForUserAllowsAdmin(t *testing.T) {
	h := NewNezhaHandler()
	h.CreateStream("alice-stream", 100)

	if !h.IsStreamAuthorizedForUser("alice-stream", 999, true) {
		t.Fatalf("admin must be authorized to attach regardless of creator")
	}
}

func TestIsStreamAuthorizedForUserDeniesUnknownStream(t *testing.T) {
	h := NewNezhaHandler()

	if h.IsStreamAuthorizedForUser("nonexistent", 100, true) {
		t.Fatalf("unknown stream id must not authorize even admin")
	}
}

// IOStream init messages begin with the magic marker ff05ff05. The inline
// check previously used && between byte inequalities, which due to short-
// circuit evaluation accepted almost every non-magic payload (any payload
// whose byte0 == 0xff was silently let through). These tests pin down the
// correct semantics: all four bytes must match exactly.
func TestIsValidIOStreamMagicAcceptsExactMagic(t *testing.T) {
	if !isValidIOStreamMagic([]byte{0xff, 0x05, 0xff, 0x05}) {
		t.Fatal("exact ff05ff05 magic must be accepted")
	}
	if !isValidIOStreamMagic([]byte{0xff, 0x05, 0xff, 0x05, 'p', 'a', 'y', 'l', 'o', 'a', 'd'}) {
		t.Fatal("ff05ff05 followed by payload must be accepted")
	}
}

func TestIsValidIOStreamMagicRejectsShortData(t *testing.T) {
	if isValidIOStreamMagic([]byte{}) {
		t.Fatal("empty data must be rejected")
	}
	if isValidIOStreamMagic([]byte{0xff, 0x05, 0xff}) {
		t.Fatal("3-byte payload must be rejected")
	}
}

func TestIsValidIOStreamMagicRejectsPartialOrWrongMagic(t *testing.T) {
	// Each case has at least one byte that does NOT match the magic. The
	// previous && short-circuit bug let cases like {0xff, 0, 0, 0} pass
	// because byte0 alone matched. Correct semantics: any single byte off
	// → reject.
	cases := [][]byte{
		{0x00, 0x00, 0x00, 0x00},
		{0xff, 0x00, 0x00, 0x00},
		{0x00, 0x05, 0x00, 0x00},
		{0x00, 0x00, 0xff, 0x00},
		{0x00, 0x00, 0x00, 0x05},
		{0xff, 0x05, 0xff, 0x00},
		{0xff, 0x05, 0x00, 0x05},
		{0xff, 0x00, 0xff, 0x05},
		{0x00, 0x05, 0xff, 0x05},
		{0xff, 0xff, 0xff, 0xff},
	}
	for _, c := range cases {
		if isValidIOStreamMagic(c) {
			t.Fatalf("non-magic payload %v must be rejected (regression: && short-circuit bug)", c)
		}
	}
}
