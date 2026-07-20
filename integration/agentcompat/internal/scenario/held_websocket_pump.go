//go:build linux

package scenario

import (
	"context"
	"errors"
	"sync"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
)

var (
	ErrInvalidHeldFramePump = errors.New("held WebSocket frame pump is invalid")
	ErrHeldFrameBufferFull  = errors.New("held WebSocket frame buffer is full")
)

type heldWebSocketPump struct {
	connection *client.WebSocketConnection
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan client.Frame
	done       chan struct{}
	stopOnce   sync.Once
	stopDone   chan struct{}
	stopResult error
	terminalMu sync.RWMutex
	terminal   error
}

func newHeldWebSocketPump(parent context.Context, connection *client.WebSocketConnection, capacity int) (*heldWebSocketPump, error) {
	if parent == nil || connection == nil || capacity < 1 {
		return nil, ErrInvalidHeldFramePump
	}
	pumpContext, cancel := context.WithCancel(parent)
	pump := &heldWebSocketPump{connection: connection, ctx: pumpContext, cancel: cancel, events: make(chan client.Frame, capacity), done: make(chan struct{}), stopDone: make(chan struct{})}
	go pump.readFrames()
	return pump, nil
}

func (pump *heldWebSocketPump) Events() <-chan client.Frame { return pump.events }

func (pump *heldWebSocketPump) Done() <-chan struct{} { return pump.done }

func (pump *heldWebSocketPump) Err() error {
	pump.terminalMu.RLock()
	defer pump.terminalMu.RUnlock()
	return pump.terminal
}

func (pump *heldWebSocketPump) readFrames() {
	defer close(pump.events)
	defer close(pump.done)
	for {
		frame, err := pump.connection.ReadFrameUntil(pump.ctx)
		if err != nil {
			if pump.ctx.Err() == nil {
				pump.setTerminal(err)
			}
			return
		}
		select {
		case pump.events <- frame:
		case <-pump.ctx.Done():
			return
		default:
			pump.setTerminal(ErrHeldFrameBufferFull)
			pump.cancel()
			_ = pump.connection.Close()
			return
		}
	}
}

func (pump *heldWebSocketPump) setTerminal(err error) {
	pump.terminalMu.Lock()
	defer pump.terminalMu.Unlock()
	if pump.terminal == nil {
		pump.terminal = err
	}
}

func (pump *heldWebSocketPump) Stop(ctx context.Context) error {
	pump.stopOnce.Do(func() {
		// The shutdown owner must outlive any individual caller's wait context.
		go pump.stop()
	})
	select {
	case <-pump.stopDone:
		return pump.stopResult
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (pump *heldWebSocketPump) stop() {
	pump.cancel()
	closeErr := pump.connection.Close()
	<-pump.done
	// Publish only after the reader is joined so every waiter observes the same complete result.
	pump.stopResult = errors.Join(pump.Err(), closeErr)
	close(pump.stopDone)
}

func (pump *heldWebSocketPump) Wait(ctx context.Context) error {
	select {
	case <-pump.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
