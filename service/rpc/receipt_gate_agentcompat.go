//go:build agentcompat

package rpc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

const receiptGateCommandTimeout = 30 * time.Second

type receiptGate struct {
	conn          net.Conn
	read          *bufio.Reader
	generation    uint64
	stateMu       sync.Mutex
	ioMu          sync.Mutex
	closeOnce     sync.Once
	context       context.Context
	cancel        context.CancelFunc
	hold          bool
	acceptedCount uint64
}

var activeReceiptGate *receiptGate
var activeReceiptGateMu sync.RWMutex
var receiptGateListener net.Listener
var receiptGateGeneration uint64
var receiptGateCancel context.CancelFunc
var receiptGateWaitGroup sync.WaitGroup

func newReceiptGate(conn net.Conn, generation uint64) *receiptGate {
	ctx, cancel := context.WithCancel(context.Background())
	return &receiptGate{conn: conn, read: bufio.NewReader(conn), generation: generation, context: ctx, cancel: cancel, hold: true}
}

func SetReceiptGateListener(listener net.Listener) {
	if listener == nil {
		return
	}
	activeReceiptGateMu.Lock()
	previousListener := receiptGateListener
	previousCancel := receiptGateCancel
	receiptGateListener = listener
	listenerContext, cancel := context.WithCancel(context.Background())
	receiptGateCancel = cancel
	activeReceiptGateMu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if previousListener != nil {
		_ = previousListener.Close()
	}
	receiptGateWaitGroup.Add(1)
	go acceptReceiptGateConnections(listenerContext, listener)
}

func acceptReceiptGateConnections(ctx context.Context, listener net.Listener) {
	defer receiptGateWaitGroup.Done()
	for {
		connection, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			return
		}
		activeReceiptGateMu.Lock()
		receiptGateGeneration++
		generation := receiptGateGeneration
		previous := activeReceiptGate
		gate := newReceiptGate(connection, generation)
		activeReceiptGate = gate
		activeReceiptGateMu.Unlock()
		if previous != nil {
			previous.close()
		}
		if err := connection.SetWriteDeadline(time.Now().Add(receiptGateCommandTimeout)); err != nil {
			resetReceiptGate(gate)
			continue
		}
		if _, err := fmt.Fprintln(connection, "ready"); err != nil {
			resetReceiptGate(gate)
			continue
		}
		_ = connection.SetWriteDeadline(time.Time{})
	}
}

func (gate *receiptGate) close() {
	gate.closeOnce.Do(func() {
		gate.cancel()
		_ = gate.conn.Close()
	})
}

func CloseReceiptGate() {
	activeReceiptGateMu.Lock()
	listener := receiptGateListener
	cancel := receiptGateCancel
	gate := activeReceiptGate
	receiptGateListener = nil
	receiptGateCancel = nil
	activeReceiptGate = nil
	activeReceiptGateMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if gate != nil {
		gate.close()
	}
	receiptGateWaitGroup.Wait()
}

func resetReceiptGate(gate *receiptGate) {
	activeReceiptGateMu.Lock()
	if activeReceiptGate == gate {
		activeReceiptGate = nil
	}
	activeReceiptGateMu.Unlock()
	gate.close()
}

func currentReceiptGate() *receiptGate {
	activeReceiptGateMu.RLock()
	defer activeReceiptGateMu.RUnlock()
	return activeReceiptGate
}

func (gate *receiptGate) sendAccepted(serverID uint64, uuid string, generation, count uint64) error {
	gate.stateMu.Lock()
	gate.acceptedCount++
	count = gate.acceptedCount
	hold := gate.hold
	gate.stateMu.Unlock()
	gate.ioMu.Lock()
	defer gate.ioMu.Unlock()
	if err := gate.conn.SetDeadline(time.Now().Add(receiptGateCommandTimeout)); err != nil {
		resetReceiptGate(gate)
		return err
	}
	if _, err := fmt.Fprintf(gate.conn, "accepted %d %s %d %d %d\n", serverID, uuid, gate.generation, generation, count); err != nil {
		resetReceiptGate(gate)
		return err
	}
	if !hold {
		_ = gate.conn.SetDeadline(time.Time{})
		return nil
	}
	command, err := gate.read.ReadString('\n')
	if err != nil {
		resetReceiptGate(gate)
		return err
	}
	if strings.TrimSpace(command) != "release" {
		err := errors.New("receipt gate received unexpected command")
		resetReceiptGate(gate)
		return err
	}
	gate.stateMu.Lock()
	gate.hold = false
	gate.stateMu.Unlock()
	if err := gate.conn.SetDeadline(time.Time{}); err != nil {
		resetReceiptGate(gate)
		return err
	}
	return nil
}

func notifyReceiptAccepted(serverID uint64, uuid string, generation, count uint64) error {
	gate := currentReceiptGate()
	if gate == nil {
		return nil
	}
	return gate.sendAccepted(serverID, uuid, generation, count)
}

func notifyStateReceived(serverID uint64, uuid string, generation, count uint64) error {
	gate := currentReceiptGate()
	if gate == nil {
		return nil
	}
	gate.ioMu.Lock()
	defer gate.ioMu.Unlock()
	if err := gate.conn.SetWriteDeadline(time.Now().Add(receiptGateCommandTimeout)); err != nil {
		resetReceiptGate(gate)
		return err
	}
	if _, err := fmt.Fprintf(gate.conn, "state %d %s %d %d\n", serverID, uuid, generation, count); err != nil {
		resetReceiptGate(gate)
		return err
	}
	return gate.conn.SetWriteDeadline(time.Time{})
}

func notifyInfo2(serverID uint64, uuid string) error {
	gate := currentReceiptGate()
	if gate == nil {
		return nil
	}
	gate.ioMu.Lock()
	defer gate.ioMu.Unlock()
	if err := gate.conn.SetWriteDeadline(time.Now().Add(receiptGateCommandTimeout)); err != nil {
		resetReceiptGate(gate)
		return err
	}
	if _, err := fmt.Fprintf(gate.conn, "info2 %d %d %s\n", gate.generation, serverID, uuid); err != nil {
		resetReceiptGate(gate)
		return err
	}
	return gate.conn.SetWriteDeadline(time.Time{})
}

func notifyMCPTaskDispatched(serverID, taskID, taskType uint64) {
	notifyMCPReceipt("task", serverID, taskID, taskType)
}

func notifyMCPTaskResultAccepted(serverID, taskID, taskType uint64) {
	notifyMCPReceipt("result", serverID, taskID, taskType)
}

func notifyMCPReceipt(kind string, serverID, taskID, taskType uint64) {
	gate := currentReceiptGate()
	if gate == nil {
		return
	}
	gate.ioMu.Lock()
	defer gate.ioMu.Unlock()
	if err := gate.conn.SetWriteDeadline(time.Now().Add(receiptGateCommandTimeout)); err != nil {
		resetReceiptGate(gate)
		return
	}
	if _, err := fmt.Fprintf(gate.conn, "%s %d %d %d %d\n", kind, gate.generation, serverID, taskID, taskType); err != nil {
		resetReceiptGate(gate)
		return
	}
	if err := gate.conn.SetWriteDeadline(time.Time{}); err != nil {
		resetReceiptGate(gate)
	}
}
