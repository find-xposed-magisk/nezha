//go:build linux

package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (dashboard *Dashboard) WaitForReceiptAccepted(ctx context.Context) error {
	if dashboard.receiptEvents == nil {
		return errors.New("receipt gate is disabled")
	}
	for {
		dashboard.eventMu.RLock()
		closed, notify := dashboard.eventClosed, dashboard.eventNotify
		dashboard.eventMu.RUnlock()
		dashboard.receiptMu.RLock()
		observed := dashboard.receiptAcceptedCount > 0
		dashboard.receiptMu.RUnlock()
		if closed {
			return ErrReceiptGateClosed
		}
		if observed {
			return nil
		}
		select {
		case <-notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (dashboard *Dashboard) WaitForSecondState(ctx context.Context) error {
	if dashboard.receiptEvents == nil {
		return errors.New("receipt gate is disabled")
	}
	for {
		dashboard.eventMu.RLock()
		closed, notify := dashboard.eventClosed, dashboard.eventNotify
		dashboard.eventMu.RUnlock()
		dashboard.receiptMu.RLock()
		observed := dashboard.receiptAcceptedCount >= 2
		dashboard.receiptMu.RUnlock()
		if closed {
			return ErrReceiptGateClosed
		}
		if observed {
			return nil
		}
		select {
		case <-notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (dashboard *Dashboard) WaitForState(ctx context.Context, want uint64) error {
	return dashboard.waitForState(ctx, 0, "", 0, want)
}

func (dashboard *Dashboard) WaitForStateGeneration(ctx context.Context, serverID uint64, uuid string, generation, want uint64) error {
	return dashboard.waitForState(ctx, serverID, uuid, generation, want)
}

func (dashboard *Dashboard) StateGeneration(serverID uint64, uuid string) uint64 {
	dashboard.stateMu.Lock()
	defer dashboard.stateMu.Unlock()
	var generation uint64
	for event := range dashboard.stateEvents {
		if event.ServerID == serverID && event.UUID == uuid && event.Generation > generation {
			generation = event.Generation
		}
	}
	return generation
}

func (dashboard *Dashboard) waitForState(ctx context.Context, serverID uint64, uuid string, generation, want uint64) error {
	if dashboard.receiptEvents == nil {
		return errors.New("receipt gate is disabled")
	}
	for {
		dashboard.eventMu.RLock()
		closed, notify := dashboard.eventClosed, dashboard.eventNotify
		dashboard.eventMu.RUnlock()
		dashboard.stateMu.Lock()
		observed := false
		for event := range dashboard.stateEvents {
			if (serverID == 0 || event.ServerID == serverID) && (uuid == "" || event.UUID == uuid) && (generation == 0 || event.Generation == generation) && event.Count == want {
				observed = true
				break
			}
		}
		dashboard.stateMu.Unlock()
		if closed {
			return ErrReceiptGateClosed
		}
		if observed {
			return nil
		}
		select {
		case <-notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (dashboard *Dashboard) WaitForInfo2(ctx context.Context, serverID uint64, uuid string) error {
	if dashboard.receiptEvents == nil {
		return errors.New("receipt gate is disabled")
	}
	want := fmt.Sprintf("info2 %d %s\n", serverID, uuid)
	for {
		dashboard.eventMu.RLock()
		closed, notify := dashboard.eventClosed, dashboard.eventNotify
		dashboard.eventMu.RUnlock()
		dashboard.info2Mu.Lock()
		_, observed := dashboard.info2Events[want]
		dashboard.info2Mu.Unlock()
		if closed {
			return ErrReceiptGateClosed
		}
		if observed {
			return nil
		}
		select {
		case <-notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (dashboard *Dashboard) WaitForInfo2UUID(ctx context.Context, uuid string) (uint64, error) {
	if dashboard.receiptEvents == nil {
		return 0, errors.New("receipt gate is disabled")
	}
	for {
		dashboard.eventMu.RLock()
		closed, notify := dashboard.eventClosed, dashboard.eventNotify
		dashboard.eventMu.RUnlock()
		dashboard.info2Mu.Lock()
		for event := range dashboard.info2Events {
			fields := strings.Fields(event)
			if len(fields) == 3 && fields[0] == "info2" && fields[2] == uuid {
				var serverID uint64
				if _, err := fmt.Sscan(fields[1], &serverID); err == nil && serverID != 0 {
					dashboard.info2Mu.Unlock()
					return serverID, nil
				}
			}
		}
		dashboard.info2Mu.Unlock()
		if closed {
			return 0, ErrReceiptGateClosed
		}
		select {
		case <-notify:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func (dashboard *Dashboard) ReceiptAccepted() bool {
	dashboard.receiptMu.RLock()
	defer dashboard.receiptMu.RUnlock()
	return dashboard.receiptAccepted
}
func (dashboard *Dashboard) ReceiptAcceptedCount() uint64 {
	dashboard.receiptMu.RLock()
	defer dashboard.receiptMu.RUnlock()
	return dashboard.receiptAcceptedCount
}
func (dashboard *Dashboard) ReceiptGeneration() uint64 {
	dashboard.receiptMu.RLock()
	defer dashboard.receiptMu.RUnlock()
	return dashboard.receiptGeneration
}
