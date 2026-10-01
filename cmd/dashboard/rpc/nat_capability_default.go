//go:build !agentcompat

package rpc

import (
	"net/http"

	"github.com/nezhahq/nezha/model"
	serviceRPC "github.com/nezhahq/nezha/service/rpc"
)

type natCapabilityLease struct {
	active           bool
	publicationOwned bool
	streamLease      *serviceRPC.AgentCompatNATStreamLease
	access           serviceRPC.AgentCompatCapabilityAccess
	handle           serviceRPC.AgentCompatNATPublishHandle
}

func prepareNATCapability(request *http.Request, _ *model.NAT) (natCapabilityLease, error) {
	// The default build has no capability protocol: every request takes the
	// legacy NAT path, so any dashboard-issued credential (panel JWT or panel
	// PAT) carried by the Authorization header, the ?token= query parameter or
	// the nz-jwt cookie is stripped before the request is tunneled to the
	// agent. Foreign values are ordinary request data and stay put.
	stripDashboardCredentials(request)
	return natCapabilityLease{}, nil
}

func (lease natCapabilityLease) cleanup(*serviceRPC.NezhaHandler) {}
