//go:build !agentcompat

package rpc

import "time"

func (*NezhaHandler) StartAgentCompatNATStream(AgentCompatNATPublishHandle, time.Duration) (bool, error) {
	return false, ErrAgentCompatCapabilityUnavailable
}
