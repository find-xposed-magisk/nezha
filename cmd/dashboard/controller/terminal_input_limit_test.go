package controller

import "testing"

func TestTerminalWebSocketInputLimitAllowsBoundedPaste(t *testing.T) {
	if terminalWebSocketInputLimit != 512*1024+64 {
		t.Fatalf("terminal WebSocket input limit = %d, want 512 KiB plus control-byte allowance", terminalWebSocketInputLimit)
	}
}
