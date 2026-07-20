//go:build agentcompat

package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

type agentcompatCapabilityIdentity struct {
	purpose    rpc.AgentCompatCapabilityPurpose
	serverID   uint64
	resourceID uint64
}

type agentcompatCapabilityProof struct {
	owner    rpc.AgentCompatCapabilityOwner
	identity agentcompatCapabilityIdentity
}

func currentAgentcompatCapabilityProof(context *gin.Context, identity agentcompatCapabilityIdentity) (agentcompatCapabilityProof, error) {
	if err := validateAgentcompatCapabilityIdentity(identity.purpose, identity.serverID, identity.resourceID); err != nil {
		return agentcompatCapabilityProof{}, err
	}
	token := APITokenFromContext(context)
	authorized, present := context.Get(model.CtxKeyAuthorizedUser)
	user, validUser := authorized.(*model.User)
	if token == nil || token.ID == 0 || !present || !validUser || user == nil || user.ID == 0 {
		return agentcompatCapabilityProof{}, errAgentcompatCapabilityUnavailable
	}
	if singleton.ServerShared == nil {
		return agentcompatCapabilityProof{}, errAgentcompatCapabilityUnavailable
	}
	server, exists := singleton.ServerShared.Get(identity.serverID)
	if !exists || server == nil || !server.HasPermission(context) || !patAllowsServer(context, identity.serverID) {
		return agentcompatCapabilityProof{}, errAgentcompatCapabilityUnavailable
	}
	if identity.purpose == rpc.AgentCompatCapabilityNAT && !currentAgentcompatNATPermission(context, identity) {
		return agentcompatCapabilityProof{}, errAgentcompatCapabilityUnavailable
	}
	return agentcompatCapabilityProof{
		owner:    rpc.AgentCompatCapabilityOwner{PATID: token.ID, UserID: user.ID, IsAdmin: user.Role.IsAdmin()},
		identity: identity,
	}, nil
}

func currentAgentcompatNATPermission(context *gin.Context, identity agentcompatCapabilityIdentity) bool {
	if singleton.NATShared == nil {
		return false
	}
	domain := singleton.NATShared.GetDomain(identity.resourceID)
	if domain == "" {
		return false
	}
	profile := singleton.NATShared.GetNATConfigByDomain(domain)
	return profile != nil && profile.ID == identity.resourceID && profile.ServerID == identity.serverID && profile.HasPermission(context)
}

func (proof agentcompatCapabilityProof) registration() rpc.AgentCompatCapabilityRegistration {
	return rpc.AgentCompatCapabilityRegistration{
		Owner: proof.owner, Purpose: proof.identity.purpose, TargetServerID: proof.identity.serverID,
		ResourceID: proof.identity.resourceID, ServerAccessAllowed: true,
	}
}

func (proof agentcompatCapabilityProof) access(capability rpc.AgentCompatIOStreamCapability) rpc.AgentCompatCapabilityAccess {
	return rpc.AgentCompatCapabilityAccess{
		Capability: capability, Owner: proof.owner, Purpose: proof.identity.purpose,
		TargetServerID: proof.identity.serverID, ResourceID: proof.identity.resourceID, ServerAccessAllowed: true,
	}
}

func agentcompatCapabilityIdentityFromWire(purpose string, serverID, resourceID uint64) (agentcompatCapabilityIdentity, error) {
	parsedPurpose, err := parseAgentcompatCapabilityPurpose(purpose)
	if err != nil {
		return agentcompatCapabilityIdentity{}, err
	}
	identity := agentcompatCapabilityIdentity{purpose: parsedPurpose, serverID: serverID, resourceID: resourceID}
	if err := validateAgentcompatCapabilityIdentity(identity.purpose, identity.serverID, identity.resourceID); err != nil {
		return agentcompatCapabilityIdentity{}, err
	}
	return identity, nil
}
