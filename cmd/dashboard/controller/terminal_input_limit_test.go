package controller

import "testing"

type readLimitRecorder struct {
	limit int64
}

func (r *readLimitRecorder) SetReadLimit(limit int64) {
	r.limit = limit
}

func TestTerminalWebSocketInputLimitAllowsBoundedPaste(t *testing.T) {
	if terminalWebSocketInputLimit != 512*1024+64 {
		t.Fatalf("terminal WebSocket input limit = %d, want 512 KiB plus control-byte allowance", terminalWebSocketInputLimit)
	}
}

func TestFileManagerWebSocketInputLimitMatchesUploadChunk(t *testing.T) {
	if fileManagerWebSocketInputLimit != 1024*1024 {
		t.Fatalf("file-manager WebSocket input limit = %d, want official client's 1 MiB upload chunk", fileManagerWebSocketInputLimit)
	}
	recorder := new(readLimitRecorder)
	limitFileManagerWebSocketInput(recorder)
	if recorder.limit != fileManagerWebSocketInputLimit {
		t.Fatalf("file-manager WebSocket configured limit = %d, want %d", recorder.limit, fileManagerWebSocketInputLimit)
	}
}
