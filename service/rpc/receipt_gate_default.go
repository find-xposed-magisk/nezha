//go:build !agentcompat

package rpc

import "net"

func SetReceiptGateListener(net.Listener) {}

func CloseReceiptGate() {}

func notifyReceiptAccepted(uint64, string, uint64, uint64) error { return nil }

func notifyStateReceived(uint64, string, uint64, uint64) error { return nil }

func notifyInfo2(uint64, string) error { return nil }

func notifyMCPTaskDispatched(uint64, uint64, uint64) {}

func notifyMCPTaskResultAccepted(uint64, uint64, uint64) {}
