//go:build agentcompat

package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	"github.com/nezhahq/nezha/service/rpc"
)

type agentcompatCapabilityHeaderContextKey struct{}

func prepareAgentcompatCapabilityHeader(c *gin.Context) {
	values := c.Request.Header.Values(agentcompatcontract.IOStreamCapabilityHeader)
	if len(values) == 0 {
		return
	}
	c.Request.Header.Del(agentcompatcontract.IOStreamCapabilityHeader)
	c.Set(agentcompatCapabilityHeaderContextKey{}, values)
}

func createIOStreamWithAgentcompatCapability(c *gin.Context, streamID string, creatorUserID, serverID uint64, purpose rpc.AgentCompatCapabilityPurpose) (func(), error) {
	identity := agentcompatCapabilityIdentity{purpose: purpose, serverID: serverID}
	rawValues, _ := c.Get(agentcompatCapabilityHeaderContextKey{})
	values, _ := rawValues.([]string)
	if len(values) == 0 {
		if err := rpc.NezhaHandlerSingleton.CreateStream(streamID, creatorUserID, serverID); err != nil {
			return nil, err
		}
		return func() { _ = rpc.NezhaHandlerSingleton.CloseStream(streamID) }, nil
	}
	if len(values) != 1 || values[0] == "" {
		return nil, errAgentcompatCapabilityUnavailable
	}
	capability, err := rpc.ParseAgentCompatIOStreamCapability(values[0])
	if err != nil {
		return nil, errAgentcompatCapabilityUnavailable
	}
	proof, err := currentAgentcompatCapabilityProof(c, identity)
	if err != nil {
		return nil, errAgentcompatCapabilityUnavailable
	}
	access := proof.access(capability)
	if err := rpc.NezhaHandlerSingleton.CreateStreamWithPurpose(streamID, creatorUserID, serverID, agentcompatStreamPurpose(purpose)); err != nil {
		_ = rpc.NezhaHandlerSingleton.UnregisterAgentCompatIOStreamCapability(access)
		return nil, err
	}
	// Bind before dispatch preserves exact cancellation when the create response is lost.
	if err := rpc.NezhaHandlerSingleton.BindAgentCompatIOStreamCapability(rpc.AgentCompatCapabilityBinding{AgentCompatCapabilityAccess: access, StreamID: streamID}); err != nil {
		_ = rpc.NezhaHandlerSingleton.CloseStream(streamID)
		return nil, errAgentcompatCapabilityUnavailable
	}
	return func() {
		_ = rpc.NezhaHandlerSingleton.CancelAgentCompatIOStreamCapability(access)
	}, nil
}

func agentcompatStreamPurpose(purpose rpc.AgentCompatCapabilityPurpose) rpc.StreamPurpose {
	if purpose == rpc.AgentCompatCapabilityTerminal {
		return rpc.PurposeTerminal
	}
	return rpc.PurposeFileManager
}
