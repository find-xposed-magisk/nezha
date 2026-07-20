package rpc

import (
	"context"
	"testing"
	"time"
)

// WaitForAgent 必须在 stream 被 RevokeStreamsForPurpose 强制下线时立刻返回，
// 否则 EnableMCP=false 的 kill switch 不能真的“立即”切断那些还卡在
// “等待 agent attach” 阶段的 transfer 请求 —— 它们会一直等到 timeout
// （生产路径上是 30 秒）。
//
// 期望行为：调用 RevokeStreamsForPurpose 后，WaitForAgent 在远小于 timeout
// 的时间内返回 (nil, false)。
func TestWaitForAgent_RevokeWakesUpWaiter(t *testing.T) {
	h := NewNezhaHandler()
	const streamID = "kill-switch-wait"
	if err := h.CreateStreamWithPurpose(streamID, 0, 7, PurposeMCPTransfer); err != nil {
		t.Fatalf("create waiter stream: %v", err)
	}

	done := make(chan struct {
		io  any
		ok  bool
		dur time.Duration
	}, 1)

	start := time.Now()
	go func() {
		// 给一个明显大于 revoke 触发延时的 timeout；如果 revoke 没唤醒，
		// WaitForAgent 会一直等到这里，下面的 assertion 就会失败。
		stream, ok := h.WaitForAgent(context.Background(), streamID, 5*time.Second)
		done <- struct {
			io  any
			ok  bool
			dur time.Duration
		}{stream, ok, time.Since(start)}
	}()
	waiter, err := h.GetStream(streamID)
	if err != nil {
		t.Fatalf("get waiter context: %v", err)
	}
	select {
	case <-waiter.waitStartedCh:
	case <-time.After(time.Second):
		t.Fatal("WaitForAgent did not enter its blocking select")
	}

	if revoked := h.RevokeStreamsForPurpose(PurposeMCPTransfer); revoked != 1 {
		t.Fatalf("expected to revoke exactly 1 MCP stream, got %d", revoked)
	}

	select {
	case res := <-done:
		if res.ok {
			t.Fatalf("WaitForAgent must return ok=false after revoke; got ok=true")
		}
		if res.dur > time.Second {
			t.Fatalf("WaitForAgent did not wake up promptly after revoke (took %s); kill switch is not immediate", res.dur)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("WaitForAgent never returned after revoke; kill switch did not wake the waiter")
	}
}

func TestRevokeStreamsForServerWakesWaitForAgentAndPreservesNewGeneration(t *testing.T) {
	h := NewNezhaHandler()
	const streamID = "server-revoke-generation"
	if err := h.CreateStream(streamID, 0, 7); err != nil {
		t.Fatalf("create waiter stream: %v", err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := h.WaitForAgent(context.Background(), streamID, time.Minute)
		done <- ok
	}()
	waiter, err := h.GetStream(streamID)
	if err != nil {
		t.Fatalf("get waiter stream: %v", err)
	}
	select {
	case <-waiter.waitStartedCh:
	case <-time.After(time.Second):
		t.Fatal("WaitForAgent did not reach its blocking select")
	}

	h.RevokeStreamsForServer(7)
	select {
	case ok := <-done:
		if ok {
			t.Fatal("WaitForAgent must return false after server revocation")
		}
	case <-time.After(time.Second):
		t.Fatal("server revocation did not wake WaitForAgent")
	}
	h.RevokeStreamsForServer(7)
	if err := h.CreateStream(streamID, 0, 8); err != nil {
		t.Fatalf("new generation must reuse released ID: %v", err)
	}
	h.RevokeStreamsForServer(7)
	if h.StreamCount() != 1 {
		t.Fatalf("new generation must remain tracked, got %d streams", h.StreamCount())
	}
	if err := h.CloseStream(streamID); err != nil {
		t.Fatalf("cleanup new generation: %v", err)
	}
}
