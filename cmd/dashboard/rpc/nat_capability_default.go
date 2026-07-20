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
	request.Header.Del("Authorization")
	return natCapabilityLease{}, nil
}

func (lease natCapabilityLease) cleanup(*serviceRPC.NezhaHandler) {}
