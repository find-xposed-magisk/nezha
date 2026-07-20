package rpc

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/goccy/go-json"

	"github.com/hashicorp/go-uuid"
	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
	"github.com/nezhahq/nezha/proto"
	serviceRPC "github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

func ServeNAT(w http.ResponseWriter, r *http.Request, natConfig *model.NAT) {
	capabilityLease, capabilityErr := prepareNATCapability(r, natConfig)
	if capabilityErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("NAT capability unavailable"))
		return
	}
	handler := serviceRPC.NezhaHandlerSingleton
	streamId := ""
	legacyStreamOwned := false
	relayCompleted := false
	defer func() {
		if capabilityLease.active {
			if !relayCompleted {
				capabilityLease.cleanup(handler)
			}
			return
		}
		if legacyStreamOwned {
			_ = handler.CloseStream(streamId)
		}
	}()
	server, _ := singleton.ServerShared.Get(natConfig.ServerID)
	if server == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("server not found or not connected"))
		return
	}
	if server.GetTaskStream() == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("server not found or not connected"))
		return
	}

	streamId, err := uuid.GenerateUUID()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(fmt.Appendf(nil, "stream id error: %v", err))
		return
	}
	if capabilityLease.active {
		capabilityLease.streamLease, err = handler.CreateAgentCompatNATStream(capabilityLease.handle, streamId)
	} else {
		err = handler.CreateStream(streamId, 0, server.ID)
	}
	if err != nil {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write(fmt.Appendf(nil, "stream limit: %v", err))
		return
	}
	legacyStreamOwned = !capabilityLease.active
	taskData, err := json.Marshal(model.TaskNAT{StreamID: streamId, Host: natConfig.Host})
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(fmt.Appendf(nil, "task data error: %v", err))
		return
	}

	if err := server.SendTask(&proto.Task{Type: model.TaskTypeNAT, Data: string(taskData)}); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(fmt.Appendf(nil, "send task error: %v", err))
		return
	}

	// Authorization authenticates Dashboard access before NAT ingress; it must not become an origin credential for the configured NAT backend.
	wWrapped, err := utils.NewRequestWrapper(r, w)
	if err != nil {
		return
	}

	if err := handler.UserConnected(streamId, wWrapped); err != nil {
		if closeErr := wWrapped.Close(); closeErr != nil {
			log.Printf("NEZHA>> NAT request wrapper close error after user connection failure: %v", closeErr)
		}
		return
	}

	if capabilityLease.active {
		if err := handler.PublishAgentCompatNATStream(capabilityLease.handle, serviceRPC.AgentCompatNATPublication{
			Purpose:        serviceRPC.AgentCompatCapabilityNAT,
			TargetServerID: server.ID,
			ResourceID:     natConfig.ID,
			StreamID:       streamId,
		}); err != nil {
			return
		}
	}
	if capabilityLease.active {
		capabilityLease.publicationOwned, err = handler.StartAgentCompatNATStream(capabilityLease.handle, time.Second*10)
		if err != nil {
			return
		}
	} else if err := handler.StartStream(streamId, time.Second*10); err != nil {
		return
	}
	relayCompleted = true
}
