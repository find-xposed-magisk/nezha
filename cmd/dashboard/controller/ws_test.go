package controller

import (
	"sync/atomic"
	"testing"
)

func TestWs(t *testing.T) {
	onlineUsers := new(atomic.Uint64)
	onlineUsers.Add(1)
	if onlineUsers.Load() != 1 {
		t.Error("onlineUsers.Add(1) failed")
	}
	onlineUsers.Add(1)
	if onlineUsers.Load() != 2 {
		t.Error("onlineUsers.Add(1) failed")
	}
	onlineUsers.Add(^uint64(0))
	if onlineUsers.Load() != 1 {
		t.Error("onlineUsers.Add(^uint64(0)) failed")
	}
	onlineUsers.Add(^uint64(0))
	if onlineUsers.Load() != 0 {
		t.Error("onlineUsers.Add(^uint64(0)) failed")
	}
}
