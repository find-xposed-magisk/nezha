package fixture

import (
	"bytes"
	"io"
	"runtime"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

type retainedHeapReader struct {
	reader   io.Reader
	baseline uint64
	peak     uint64
}

func verifyPayloadPeakRetainedHeap(reader io.Reader, expectedBytes uint64) (PayloadDigest, uint64, error) {
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	measuredReader := &retainedHeapReader{reader: reader, baseline: baseline.HeapAlloc}
	digest, err := VerifyPayload(measuredReader, expectedBytes)
	return digest, measuredReader.peak, err
}

func (reader *retainedHeapReader) Read(destination []byte) (int, error) {
	readBytes, err := reader.reader.Read(destination)
	// Sampling after a forced collection measures the live heap retained by each
	// streaming checkpoint instead of scheduler-dependent allocation churn.
	runtime.GC()
	var sample runtime.MemStats
	runtime.ReadMemStats(&sample)
	runtime.KeepAlive(destination)
	if sample.HeapAlloc > reader.baseline {
		reader.peak = max(reader.peak, sample.HeapAlloc-reader.baseline)
	}
	return readBytes, err
}

func TestFixture_PeakRetainedHeapDetectsRetainedAllocation(t *testing.T) {
	// Given
	reader := &allocationRetainingReader{reader: bytes.NewReader([]byte("x"))}

	// When
	_, peakRetainedHeap, err := verifyPayloadPeakRetainedHeap(reader, 1)
	requireNoFixtureError(t, err)

	// Then
	if peakRetainedHeap <= contract.TransferHeapBytes {
		t.Fatalf("peak retained heap = %d, want greater than %d", peakRetainedHeap, contract.TransferHeapBytes)
	}
}

type allocationRetainingReader struct {
	reader   io.Reader
	retained []byte
}

func (reader *allocationRetainingReader) Read(destination []byte) (int, error) {
	if reader.retained == nil {
		reader.retained = make([]byte, contract.TransferHeapBytes+1024*1024)
	}
	return reader.reader.Read(destination)
}
