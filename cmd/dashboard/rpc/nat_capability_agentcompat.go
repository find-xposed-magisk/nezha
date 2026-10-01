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
		// Authorization values (panel JWT or panel PAT) so they never become
		// origin credentials; foreign ones are ordinary request data.
		stripDashboardCredential(request)
		return natCapabilityLease{}, nil
	}
	request.Header.Del(agentcompatcontract.IOStreamCapabilityHeader)
	if len(values) != 1 || values[0] == "" {
		// Invalid capability: nothing is forwarded, but dashboard-issued
		// credentials still must not survive into errors or task data.
		stripDashboardCredential(request)
		return natCapabilityLease{}, errors.New("invalid NAT capability")
	}
	access, handle, err := serviceRPC.NezhaHandlerSingleton.ConsumeAgentCompatNATCapabilityForProfile(values[0], natConfig.ServerID, natConfig.ID)
	// From here the request bytes may reach the agent (active lease) or the
	// request ends in a 503; either way dashboard-issued Authorization values
	// are stripped, foreign ones are forwarded untouched.
	stripDashboardCredential(request)
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
