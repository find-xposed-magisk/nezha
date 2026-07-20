//go:build agentcompat

package rpc

import (
	"encoding/binary"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func setUniqueAgentCompatCapabilityTokens(handler *NezhaHandler) *atomic.Uint64 {
	var issued atomic.Uint64
	handler.setAgentCompatCapabilityTokenSourceForTest(func(destination []byte) error {
		binary.LittleEndian.PutUint64(destination, issued.Add(1))
		return nil
	})
	return &issued
}

func registerAgentCompatCapability(t *testing.T, handler *NezhaHandler, registration AgentCompatCapabilityRegistration) AgentCompatIOStreamCapability {
	t.Helper()
	capability, err := handler.RegisterAgentCompatIOStreamCapability(agentCompatCapabilityTestContext(t), registration)
	require.NoError(t, err)
	return capability
}

func agentCompatCapabilityRegistryCounts(handler *NezhaHandler) (active, used int) {
	handler.ioStreamMutex.RLock()
	defer handler.ioStreamMutex.RUnlock()
	return len(handler.agentCompatCapabilities.active), len(handler.agentCompatCapabilities.used)
}

func agentCompatCapabilityActiveForPAT(handler *NezhaHandler, patID uint64) (uint16, bool) {
	handler.ioStreamMutex.RLock()
	defer handler.ioStreamMutex.RUnlock()
	active, exists := handler.agentCompatCapabilities.activeByPAT[patID]
	return active, exists
}
