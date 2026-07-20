//go:build linux

package scenario

import (
	"bytes"
	"encoding/binary"
	"errors"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

var (
	errLegacyFMInvalidFrame = errors.New("legacy FM invalid frame")
	errLegacyFMUnexpected   = errors.New("legacy FM unexpected frame")
)

type legacyFMRemoteError struct{}

func (legacyFMRemoteError) Error() string { return "legacy FM agent error" }

var errLegacyFMRemote = legacyFMRemoteError{}

const (
	legacyFMListOp     byte = 0x00
	legacyFMDownloadOp byte = 0x01
	legacyFMUploadOp   byte = 0x02
)

type legacyFMEntry struct {
	Name string
	Dir  bool
}

type legacyFMList struct {
	Path    string
	Entries []legacyFMEntry
}

type legacyFMDownloadHeader struct {
	Size uint64
}

func buildLegacyFMList(path fixture.AgentPath) []byte {
	return append([]byte{legacyFMListOp}, []byte(path.String())...)
}

func buildLegacyFMUpload(path fixture.AgentPath, size uint64) []byte {
	frame := make([]byte, 1+8+len(path.String()))
	frame[0] = legacyFMUploadOp
	binary.BigEndian.PutUint64(frame[1:9], size)
	copy(frame[9:], path.String())
	return frame
}

func buildLegacyFMDownload(path fixture.AgentPath) []byte {
	return append([]byte{legacyFMDownloadOp}, []byte(path.String())...)
}

func parseLegacyFMList(frame []byte) (legacyFMList, error) {
	if message, ok := parseLegacyFMError(frame); ok {
		return legacyFMList{}, message
	}
	if len(frame) < 8 || !bytes.Equal(frame[:4], []byte("NZFN")) {
		return legacyFMList{}, errLegacyFMInvalidFrame
	}
	pathSize := binary.BigEndian.Uint32(frame[4:8])
	if pathSize == 0 || uint64(pathSize)+8 > uint64(len(frame)) {
		return legacyFMList{}, errLegacyFMInvalidFrame
	}
	pathEnd := 8 + int(pathSize)
	result := legacyFMList{Path: string(frame[8:pathEnd])}
	for cursor := pathEnd; cursor < len(frame); {
		if len(frame)-cursor < 2 {
			return legacyFMList{}, errLegacyFMInvalidFrame
		}
		entrySize := int(frame[cursor+1])
		if entrySize == 0 || cursor+2+entrySize > len(frame) || frame[cursor] > 1 {
			return legacyFMList{}, errLegacyFMInvalidFrame
		}
		result.Entries = append(result.Entries, legacyFMEntry{
			Name: string(frame[cursor+2 : cursor+2+entrySize]),
			Dir:  frame[cursor] == 1,
		})
		cursor += 2 + entrySize
	}
	return result, nil
}

func parseLegacyFMDownload(frame []byte) (legacyFMDownloadHeader, error) {
	if message, ok := parseLegacyFMError(frame); ok {
		return legacyFMDownloadHeader{}, message
	}
	if len(frame) != 12 || !bytes.Equal(frame[:4], []byte("NZTD")) {
		return legacyFMDownloadHeader{}, errLegacyFMInvalidFrame
	}
	return legacyFMDownloadHeader{Size: binary.BigEndian.Uint64(frame[4:12])}, nil
}

func parseLegacyFMError(frame []byte) (error, bool) {
	if len(frame) < 4 || !bytes.Equal(frame[:4], []byte("NERR")) {
		return nil, false
	}
	return errLegacyFMRemote, true
}

func requireLegacyFMMarker(frame []byte, marker string) error {
	if message, ok := parseLegacyFMError(frame); ok {
		return message
	}
	if !bytes.Equal(frame, []byte(marker)) {
		return errLegacyFMUnexpected
	}
	return nil
}
