//go:build !agentcompat

package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/service/rpc"
)

func prepareAgentcompatCapabilityHeader(*gin.Context) {}

func createIOStreamWithAgentcompatCapability(_ *gin.Context, streamID string, creatorUserID, serverID uint64, _ rpc.AgentCompatCapabilityPurpose) (func(), error) {
	if err := rpc.NezhaHandlerSingleton.CreateStream(streamID, creatorUserID, serverID); err != nil {
		return nil, err
	}
	return func() { _ = rpc.NezhaHandlerSingleton.CloseStream(streamID) }, nil
}
