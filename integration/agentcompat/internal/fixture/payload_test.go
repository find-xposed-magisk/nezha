package fixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestFixture_StreamsExact100MiB(t *testing.T) {
	// Given
	payload, err := NewPayload(contract.DefaultSeed, contract.TransferBytes)
	requireNoFixtureError(t, err)

	// When
	digest, peakRetainedHeap, err := verifyPayloadPeakRetainedHeap(payload.Reader(), contract.TransferBytes)
	requireNoFixtureError(t, err)
	stableDigest, err := VerifyPayload(payload.Reader(), contract.TransferBytes)
	requireNoFixtureError(t, err)
	independentDigest := independentlyHashPayload(contract.DefaultSeed, contract.TransferBytes)

	// Then
	if digest.Bytes != contract.TransferBytes {
		t.Fatalf("payload bytes = %d", digest.Bytes)
	}
	if digest.SHA256 != stableDigest.SHA256 {
		t.Fatalf("payload SHA changed: %s != %s", digest.Hex(), stableDigest.Hex())
	}
	if digest.SHA256 != independentDigest {
		t.Fatalf("payload SHA does not match independent generator: %s", digest.Hex())
	}
	if peakRetainedHeap == 0 {
		t.Fatal("peak retained heap measurement did not observe live streaming allocations")
	}
	if peakRetainedHeap > contract.TransferHeapBytes {
		t.Fatalf("peak retained heap = %d, limit = %d", peakRetainedHeap, contract.TransferHeapBytes)
	}
	t.Logf("bytes=%d sha256=%s peak_retained_heap=%d", digest.Bytes, digest.Hex(), peakRetainedHeap)
}

func independentlyHashPayload(seed contract.Seed, size uint64) [sha256.Size]byte {
	hash := sha256.New()
	var input [16]byte
	var remaining = size
	for blockIndex := uint64(0); remaining > 0; blockIndex++ {
		binary.BigEndian.PutUint64(input[:8], uint64(seed))
		binary.BigEndian.PutUint64(input[8:], blockIndex)
		block := sha256.Sum256(input[:])
		writeBytes := uint64(len(block))
		if remaining < writeBytes {
			writeBytes = remaining
		}
		_, _ = hash.Write(block[:writeBytes])
		remaining -= writeBytes
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func TestFixture_RejectsPayloadOverrun(t *testing.T) {
	// Given
	_, constructorErr := NewPayload(contract.DefaultSeed, contract.TransferBytes+1)

	// When
	_, verifierErr := VerifyPayload(bytes.NewReader([]byte("overrun")), 1)
	_, declaredSizeErr := VerifyPayload(bytes.NewReader(nil), contract.TransferBytes+1)

	// Then
	if !errors.Is(constructorErr, ErrPayloadOverrun) {
		t.Fatalf("constructor error = %v", constructorErr)
	}
	if !errors.Is(verifierErr, ErrPayloadOverrun) {
		t.Fatalf("verifier error = %v", verifierErr)
	}
	if !errors.Is(declaredSizeErr, ErrPayloadOverrun) {
		t.Fatalf("declared size error = %v", declaredSizeErr)
	}
}

func TestFixture_PayloadChunkIndependence(t *testing.T) {
	// Given
	const size = 64 * 1024
	payload, err := NewPayload(contract.DefaultSeed, size)
	requireNoFixtureError(t, err)
	chunkSizes := []int{1, 1024, 1024 * 1024, 7919}
	var baseline []byte

	for _, chunkSize := range chunkSizes {
		// When
		content := readPayloadWithChunkSize(t, payload.Reader(), chunkSize)

		// Then
		if baseline == nil {
			baseline = content
			continue
		}
		if !bytes.Equal(content, baseline) {
			t.Fatalf("payload changed with chunk size %d", chunkSize)
		}
	}
}

func TestFixture_PayloadDigestStableAtBoundaries(t *testing.T) {
	for _, size := range []uint64{0, 1, 1024 * 1024} {
		t.Run(fmt.Sprintf("bytes_%d", size), func(t *testing.T) {
			payload, err := NewPayload(contract.DefaultSeed, size)
			requireNoFixtureError(t, err)
			first, err := VerifyPayload(payload.Reader(), size)
			requireNoFixtureError(t, err)
			second, err := VerifyPayload(payload.Reader(), size)
			requireNoFixtureError(t, err)
			if first != second || first.Bytes != size {
				t.Fatalf("digest unstable at %d bytes: %+v != %+v", size, first, second)
			}
		})
	}
}

func TestFixture_VerifierRejectsShortPayload(t *testing.T) {
	_, err := VerifyPayload(bytes.NewReader([]byte("short")), 6)
	if !errors.Is(err, ErrPayloadSizeMismatch) {
		t.Fatalf("short payload error = %v", err)
	}
}

func readPayloadWithChunkSize(t *testing.T, reader io.Reader, chunkSize int) []byte {
	t.Helper()
	buffer := make([]byte, chunkSize)
	var content bytes.Buffer
	for {
		readBytes, err := reader.Read(buffer)
		if readBytes > 0 {
			_, writeErr := content.Write(buffer[:readBytes])
			requireNoFixtureError(t, writeErr)
		}
		if errors.Is(err, io.EOF) {
			return content.Bytes()
		}
		requireNoFixtureError(t, err)
	}
}
