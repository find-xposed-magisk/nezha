//go:build agentcompat

package rpc

import (
	"errors"
	"net/http"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	serviceRPC "github.com/nezhahq/nezha/service/rpc"
)

type natCapabilityLease struct {
	active           bool
	publicationOwned bool
	streamLease      *serviceRPC.AgentCompatNATStreamLease
	access           serviceRPC.AgentCompatCapabilityAccess
	handle           serviceRPC.AgentCompatNATPublishHandle
}

func prepareNATCapability(request *http.Request, natConfig *model.NAT) (natCapabilityLease, error) {
	values := request.Header.Values(agentcompatcontract.IOStreamCapabilityHeader)
	if len(values) == 0 {
		// No capability header: legacy NAT path. Strip dashboard-issued
		// credentials (panel JWT or panel PAT) from the Authorization header,
		// the ?token= query parameter and the nz-jwt cookie so they never
		// become origin credentials; foreign values are ordinary request data.
		stripDashboardCredentials(request)
		return natCapabilityLease{}, nil
	}
	request.Header.Del(agentcompatcontract.IOStreamCapabilityHeader)
	if len(values) != 1 || values[0] == "" {
		// Invalid capability: nothing is forwarded, but dashboard-issued
		// credentials still must not survive into errors or task data.
		stripDashboardCredentials(request)
		return natCapabilityLease{}, errors.New("invalid NAT capability")
	}
	access, handle, err := serviceRPC.NezhaHandlerSingleton.ConsumeAgentCompatNATCapabilityForProfile(values[0], natConfig.ServerID, natConfig.ID)
	// From here the request bytes may reach the agent (active lease) or the
	// request ends in a 503; either way dashboard-issued credentials are
	// stripped from every TokenLookup channel, foreign ones are forwarded
	// untouched.
	stripDashboardCredentials(request)
	if err != nil {
		return natCapabilityLease{}, errors.New("invalid NAT capability")
	}
	return natCapabilityLease{active: true, access: access, handle: handle}, nil
}

func (lease natCapabilityLease) cleanup(handler *serviceRPC.NezhaHandler) {
	if !lease.active {
		return
	}
	_ = handler.CancelAgentCompatIOStreamCapability(lease.access)
	_ = handler.CloseAgentCompatNATStreamLease(lease.streamLease)
	_ = handler.UnregisterAgentCompatIOStreamCapability(lease.access)
}
