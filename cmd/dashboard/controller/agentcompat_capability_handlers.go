//go:build agentcompat

package controller

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/service/rpc"
)

func registerAgentcompatCapabilityRoutes(router *gin.Engine, patAuth gin.HandlerFunc) {
	router.POST(agentcompatCapabilityRegisterPath, patAuth, commonHandler(agentcompatCapabilityRegister))
	router.POST(agentcompatCapabilityWaitPath, patAuth, commonHandler(agentcompatCapabilityWait))
	router.POST(agentcompatCapabilityCancelPath, patAuth, commonHandler(agentcompatCapabilityCancel))
	router.POST(agentcompatCapabilityUnregisterPath, patAuth, commonHandler(agentcompatCapabilityUnregister))
}

func agentcompatCapabilityRegister(context *gin.Context) (agentcompatCapabilityRegisterResponse, error) {
	var request agentcompatCapabilityRegisterRequest
	if err := decodeAgentcompatCapabilityRequest(context, &request); err != nil {
		return agentcompatCapabilityRegisterResponse{}, err
	}
	identity, err := agentcompatCapabilityIdentityFromWire(request.Purpose, request.ServerID, request.ResourceID)
	if err != nil {
		return agentcompatCapabilityRegisterResponse{}, err
	}
	proof, err := currentAgentcompatCapabilityProof(context, identity)
	if err != nil || rpc.NezhaHandlerSingleton == nil {
		return agentcompatCapabilityRegisterResponse{}, errAgentcompatCapabilityUnavailable
	}
	capability, err := rpc.NezhaHandlerSingleton.RegisterAgentCompatIOStreamCapability(context.Request.Context(), proof.registration())
	if err != nil {
		if contextError := context.Request.Context().Err(); contextError != nil {
			return agentcompatCapabilityRegisterResponse{}, contextError
		}
		return agentcompatCapabilityRegisterResponse{}, errAgentcompatCapabilityUnavailable
	}
	return agentcompatCapabilityRegisterResponse{Capability: capability.String()}, nil
}

func agentcompatCapabilityWait(context *gin.Context) (agentcompatCapabilityWaitResponse, error) {
	var request agentcompatCapabilityWaitRequest
	if err := decodeAgentcompatCapabilityRequest(context, &request); err != nil {
		return agentcompatCapabilityWaitResponse{}, err
	}
	access, err := currentAgentcompatCapabilityAccess(context, request)
	if errors.Is(err, errAgentcompatCapabilityInvalid) {
		return agentcompatCapabilityWaitResponse{}, errAgentcompatCapabilityInvalid
	}
	if err != nil || rpc.NezhaHandlerSingleton == nil {
		return agentcompatCapabilityWaitResponse{}, errAgentcompatCapabilityUnavailable
	}
	streamID, err := rpc.NezhaHandlerSingleton.WaitAgentCompatIOStreamCapability(context.Request.Context(), access)
	if err != nil {
		if contextError := context.Request.Context().Err(); contextError != nil {
			return agentcompatCapabilityWaitResponse{}, contextError
		}
		return agentcompatCapabilityWaitResponse{}, errAgentcompatCapabilityUnavailable
	}
	return agentcompatCapabilityWaitResponse{StreamID: streamID}, nil
}

func agentcompatCapabilityCancel(context *gin.Context) (agentcompatCapabilityEmptyResponse, error) {
	access, available := inertAgentcompatCapabilityAccess(context)
	if !available || rpc.NezhaHandlerSingleton == nil {
		return agentcompatCapabilityEmptyResponse{}, nil
	}
	if err := rpc.NezhaHandlerSingleton.CancelAgentCompatIOStreamCapability(access); err != nil {
		return agentcompatCapabilityEmptyResponse{}, errAgentcompatCapabilityCleanup
	}
	return agentcompatCapabilityEmptyResponse{}, nil
}

func agentcompatCapabilityUnregister(context *gin.Context) (agentcompatCapabilityEmptyResponse, error) {
	access, available := inertAgentcompatCapabilityAccess(context)
	if !available || rpc.NezhaHandlerSingleton == nil {
		return agentcompatCapabilityEmptyResponse{}, nil
	}
	err := rpc.NezhaHandlerSingleton.UnregisterAgentCompatIOStreamCapability(access)
	if errors.Is(err, rpc.ErrAgentCompatCapabilityBound) {
		return agentcompatCapabilityEmptyResponse{}, errAgentcompatCapabilityConflict
	}
	if err != nil {
		return agentcompatCapabilityEmptyResponse{}, errAgentcompatCapabilityUnavailable
	}
	return agentcompatCapabilityEmptyResponse{}, nil
}

func currentAgentcompatCapabilityAccess(context *gin.Context, request agentcompatCapabilityAccessRequest) (rpc.AgentCompatCapabilityAccess, error) {
	identity, err := agentcompatCapabilityIdentityFromWire(request.Purpose, request.ServerID, request.ResourceID)
	if err != nil {
		return rpc.AgentCompatCapabilityAccess{}, err
	}
	capability, err := rpc.ParseAgentCompatIOStreamCapability(request.Capability)
	if err != nil {
		return rpc.AgentCompatCapabilityAccess{}, errAgentcompatCapabilityUnavailable
	}
	proof, err := currentAgentcompatCapabilityProof(context, identity)
	if err != nil {
		return rpc.AgentCompatCapabilityAccess{}, errAgentcompatCapabilityUnavailable
	}
	return proof.access(capability), nil
}

func inertAgentcompatCapabilityAccess(context *gin.Context) (rpc.AgentCompatCapabilityAccess, bool) {
	var request agentcompatCapabilityAccessRequest
	if decodeAgentcompatCapabilityRequest(context, &request) != nil {
		return rpc.AgentCompatCapabilityAccess{}, false
	}
	access, err := currentAgentcompatCapabilityAccess(context, request)
	return access, err == nil
}
