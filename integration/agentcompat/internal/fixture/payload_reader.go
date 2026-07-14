package fixture

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

const payloadBlockSize = sha256.Size

type Payload struct {
	seed contract.Seed
	size uint64
}

type payloadReader struct {
	payload Payload
	offset  uint64
}

func NewPayload(seed contract.Seed, size uint64) (Payload, error) {
	if seed == 0 {
		return Payload{}, fmt.Errorf("payload seed must be nonzero")
	}
	if size > contract.TransferBytes {
		return Payload{}, ErrPayloadOverrun
	}
	return Payload{seed: seed, size: size}, nil
}

func (p Payload) Reader() io.Reader {
	return &payloadReader{payload: p}
}

func (r *payloadReader) Read(destination []byte) (int, error) {
	if r.offset >= r.payload.size {
		return 0, io.EOF
	}
	remaining := r.payload.size - r.offset
	if uint64(len(destination)) > remaining {
		destination = destination[:remaining]
	}
	written := fillPayload(destination, r.payload.seed, r.offset)
	r.offset += uint64(written)
	if r.offset == r.payload.size {
		return written, io.EOF
	}
	return written, nil
}

func fillPayload(destination []byte, seed contract.Seed, offset uint64) int {
	written := 0
	for written < len(destination) {
		absoluteOffset := offset + uint64(written)
		blockIndex := absoluteOffset / payloadBlockSize
		blockOffset := absoluteOffset % payloadBlockSize
		var input [16]byte
		binary.BigEndian.PutUint64(input[:8], uint64(seed))
		binary.BigEndian.PutUint64(input[8:], blockIndex)
		block := sha256.Sum256(input[:])
		copied := copy(destination[written:], block[blockOffset:])
		written += copied
	}
	return written
}
