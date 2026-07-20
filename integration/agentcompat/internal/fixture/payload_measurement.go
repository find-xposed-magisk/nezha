package fixture

import (
	"crypto/sha256"
	"hash"
	"io"
	"runtime"
)

type PayloadMeasurement struct {
	Digest            PayloadDigest
	RetainedHeapBytes uint64
	Chunks            uint64
}

type RetainedHeapProbe struct {
	baseline uint64
}

func NewRetainedHeapProbe() RetainedHeapProbe {
	runtime.GC()
	var sample runtime.MemStats
	runtime.ReadMemStats(&sample)
	return RetainedHeapProbe{baseline: sample.HeapAlloc}
}

func (probe RetainedHeapProbe) RetainedBytes() uint64 {
	runtime.GC()
	var sample runtime.MemStats
	runtime.ReadMemStats(&sample)
	if sample.HeapAlloc <= probe.baseline {
		return 0
	}
	return sample.HeapAlloc - probe.baseline
}

type MeasuredReader struct {
	reader io.Reader
	hash   hash.Hash
	bytes  uint64
	chunks uint64
}

func NewMeasuredReader(reader io.Reader) *MeasuredReader {
	return &MeasuredReader{reader: reader, hash: sha256.New()}
}

func (reader *MeasuredReader) Read(destination []byte) (int, error) {
	readBytes, err := reader.reader.Read(destination)
	if readBytes > 0 {
		_, _ = reader.hash.Write(destination[:readBytes])
		reader.bytes += uint64(readBytes)
		reader.chunks++
	}
	return readBytes, err
}

func (reader *MeasuredReader) Measurement() PayloadMeasurement {
	return newPayloadMeasurement(reader.hash, reader.bytes, reader.chunks)
}

type MeasuredWriter struct {
	hash   hash.Hash
	bytes  uint64
	chunks uint64
}

func NewMeasuredWriter() *MeasuredWriter {
	return &MeasuredWriter{hash: sha256.New()}
}

func (writer *MeasuredWriter) Write(payload []byte) (int, error) {
	written, err := writer.hash.Write(payload)
	if written > 0 {
		writer.bytes += uint64(written)
		writer.chunks++
	}
	return written, err
}

func (writer *MeasuredWriter) Measurement() PayloadMeasurement {
	return newPayloadMeasurement(writer.hash, writer.bytes, writer.chunks)
}

func newPayloadMeasurement(payloadHash hash.Hash, bytes, chunks uint64) PayloadMeasurement {
	digest := PayloadDigest{Bytes: bytes}
	copy(digest.SHA256[:], payloadHash.Sum(nil))
	return PayloadMeasurement{Digest: digest, Chunks: chunks}
}
