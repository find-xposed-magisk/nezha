//go:build linux

package scenario

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

const heldTerminalProofLimit = 64 << 10

type heldTerminalProof struct {
	marker string
	buffer []byte
	rows   uint32
	cols   uint32
	framed bool
	closed error
	frames uint64
	bytes  uint64
	first  client.FrameType
}

func newHeldTerminalProof(marker string) *heldTerminalProof {
	return &heldTerminalProof{marker: marker}
}

func (proof *heldTerminalProof) Consume(frame client.Frame) {
	if proof.Complete() || proof.closed != nil {
		return
	}
	proof.frames++
	proof.bytes += uint64(len(frame.Payload))
	if proof.first == "" {
		proof.first = frame.Type
	}
	proof.buffer = append(proof.buffer, frame.Payload...)
	if len(proof.buffer) > heldTerminalProofLimit {
		proof.buffer = proof.buffer[len(proof.buffer)-heldTerminalProofLimit:]
	}
	proof.consumeRecord()
}

func (proof *heldTerminalProof) consumeRecord() {
	for {
		start := bytes.IndexByte(proof.buffer, 0x1e)
		if start < 0 {
			proof.buffer = nil
			return
		}
		proof.buffer = proof.buffer[start:]
		endOffset := bytes.IndexByte(proof.buffer[1:], 0x1f)
		if endOffset < 0 {
			return
		}
		end := 1 + endOffset
		record := string(proof.buffer[1:end])
		proof.buffer = proof.buffer[end+1:]
		recordMarker, sizeText, ok := strings.Cut(record, "|")
		if !ok || recordMarker != proof.marker {
			continue
		}
		values := strings.Fields(sizeText)
		if len(values) != 2 {
			continue
		}
		rows, rowsErr := strconv.ParseUint(values[0], 10, 32)
		cols, colsErr := strconv.ParseUint(values[1], 10, 32)
		if rowsErr != nil || colsErr != nil || rows == 0 || cols == 0 {
			continue
		}
		proof.rows, proof.cols, proof.framed = uint32(rows), uint32(cols), true
		return
	}
}

func (proof *heldTerminalProof) Closed(err error) error {
	proof.closed = err
	return fmt.Errorf("terminal proof closed: %w", err)
}

func (proof *heldTerminalProof) Failure() error {
	if proof.closed == nil {
		return nil
	}
	return fmt.Errorf("terminal proof closed: %w", proof.closed)
}

func (proof *heldTerminalProof) Complete() bool {
	return proof.framed && proof.rows == 43 && proof.cols == 132
}

func (proof *heldTerminalProof) Rows() uint32 { return proof.rows }

func (proof *heldTerminalProof) Columns() uint32 { return proof.cols }

func (proof *heldTerminalProof) FrameCount() uint64 { return proof.frames }

func (proof *heldTerminalProof) ByteCount() uint64 { return proof.bytes }

func (proof *heldTerminalProof) FirstFrameType() client.FrameType { return proof.first }

func heldTerminalCommand(marker string) string {
	part := len(marker) / 2
	return fmt.Sprintf("printf '\\036%%s%%s|' '%s' '%s'; stty size; printf '\\037'; read -r held_terminal_release; exit\n", marker[:part], marker[part:])
}

func hasHeldTerminalMarker(output []byte, marker string) bool {
	return strings.Contains(string(output), marker)
}
