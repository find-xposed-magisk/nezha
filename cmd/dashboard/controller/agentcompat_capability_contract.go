//go:build agentcompat

package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/service/rpc"
)

const (
	agentcompatCapabilityRequestMaxBytes = 512

	agentcompatCapabilityRegisterPath   = "/agentcompat/io-stream-capability/register"
	agentcompatCapabilityWaitPath       = "/agentcompat/io-stream-capability/wait"
	agentcompatCapabilityCancelPath     = "/agentcompat/io-stream-capability/cancel"
	agentcompatCapabilityUnregisterPath = "/agentcompat/io-stream-capability/unregister"
)

const (
	agentcompatCapabilityPurposeTerminal    = "terminal"
	agentcompatCapabilityPurposeFileManager = "file_manager"
	agentcompatCapabilityPurposeNAT         = "nat"
)

var (
	errAgentcompatCapabilityInvalid     = errors.New("agentcompat capability request is invalid")
	errAgentcompatCapabilityUnavailable = errors.New("agentcompat capability is not available")
	errAgentcompatCapabilityConflict    = errors.New("agentcompat capability is active")
	errAgentcompatCapabilityCleanup     = errors.New("agentcompat capability cleanup failed")
)

type agentcompatCapabilityRegisterRequest struct {
	Purpose    string `json:"purpose"`
	ServerID   uint64 `json:"server_id"`
	ResourceID uint64 `json:"resource_id,omitempty"`
}

type agentcompatCapabilityRegisterResponse struct {
	Capability string `json:"capability"`
}

type agentcompatCapabilityAccessRequest struct {
	Capability string `json:"capability"`
	Purpose    string `json:"purpose"`
	ServerID   uint64 `json:"server_id"`
	ResourceID uint64 `json:"resource_id,omitempty"`
}

type agentcompatCapabilityWaitRequest = agentcompatCapabilityAccessRequest
type agentcompatCapabilityCancelRequest = agentcompatCapabilityAccessRequest
type agentcompatCapabilityUnregisterRequest = agentcompatCapabilityAccessRequest

type agentcompatCapabilityWaitResponse struct {
	StreamID string `json:"stream_id"`
}

type agentcompatCapabilityEmptyResponse struct{}

func decodeAgentcompatCapabilityRequest[T any](context *gin.Context, destination *T) error {
	context.Request.Body = http.MaxBytesReader(context.Writer, context.Request.Body, agentcompatCapabilityRequestMaxBytes)
	decoder := json.NewDecoder(context.Request.Body)
	fields, err := decodeAgentcompatCapabilityObject(decoder)
	if err != nil {
		return errAgentcompatCapabilityInvalid
	}
	switch request := any(destination).(type) {
	case *agentcompatCapabilityRegisterRequest:
		request.Purpose, err = decodeAgentcompatCapabilityString(fields.purpose)
		if err == nil {
			request.ServerID, err = decodeAgentcompatCapabilityUint64(fields.serverID)
		}
		if err == nil && fields.resourceID != nil {
			request.ResourceID, err = decodeAgentcompatCapabilityUint64(fields.resourceID)
		}
		if err != nil || fields.purpose == nil || fields.serverID == nil {
			return errAgentcompatCapabilityInvalid
		}
	case *agentcompatCapabilityAccessRequest:
		request.Capability, err = decodeAgentcompatCapabilityString(fields.capability)
		if err == nil {
			request.Purpose, err = decodeAgentcompatCapabilityString(fields.purpose)
		}
		if err == nil {
			request.ServerID, err = decodeAgentcompatCapabilityUint64(fields.serverID)
		}
		if err == nil && fields.resourceID != nil {
			request.ResourceID, err = decodeAgentcompatCapabilityUint64(fields.resourceID)
		}
		if err != nil || fields.capability == nil || fields.purpose == nil || fields.serverID == nil {
			return errAgentcompatCapabilityInvalid
		}
	default:
		return errAgentcompatCapabilityInvalid
	}
	return nil
}

type agentcompatCapabilityFields struct {
	purpose    json.RawMessage
	serverID   json.RawMessage
	resourceID json.RawMessage
	capability json.RawMessage
	seen       uint8
}

func decodeAgentcompatCapabilityObject(decoder *json.Decoder) (agentcompatCapabilityFields, error) {
	var fields agentcompatCapabilityFields
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fields, errAgentcompatCapabilityInvalid
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, validKey := keyToken.(string)
		if err != nil || !validKey {
			return fields, errAgentcompatCapabilityInvalid
		}
		var bit uint8
		var target *json.RawMessage
		switch key {
		case "purpose":
			bit, target = 1, &fields.purpose
		case "server_id":
			bit, target = 2, &fields.serverID
		case "resource_id":
			bit, target = 4, &fields.resourceID
		case "capability":
			bit, target = 8, &fields.capability
		default:
			return fields, errAgentcompatCapabilityInvalid
		}
		if fields.seen&bit != 0 || decoder.Decode(target) != nil {
			return fields, errAgentcompatCapabilityInvalid
		}
		fields.seen |= bit
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return fields, errAgentcompatCapabilityInvalid
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return fields, errAgentcompatCapabilityInvalid
	}
	return fields, nil
}

func decodeAgentcompatCapabilityString(raw json.RawMessage) (string, error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return "", errAgentcompatCapabilityInvalid
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", errAgentcompatCapabilityInvalid
	}
	return value, nil
}

func decodeAgentcompatCapabilityUint64(raw json.RawMessage) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
}

func parseAgentcompatCapabilityPurpose(value string) (rpc.AgentCompatCapabilityPurpose, error) {
	switch value {
	case agentcompatCapabilityPurposeTerminal:
		return rpc.AgentCompatCapabilityTerminal, nil
	case agentcompatCapabilityPurposeFileManager:
		return rpc.AgentCompatCapabilityFileManager, nil
	case agentcompatCapabilityPurposeNAT:
		return rpc.AgentCompatCapabilityNAT, nil
	default:
		return 0, errAgentcompatCapabilityInvalid
	}
}

func validateAgentcompatCapabilityIdentity(purpose rpc.AgentCompatCapabilityPurpose, serverID, resourceID uint64) error {
	if serverID == 0 {
		return errAgentcompatCapabilityInvalid
	}
	switch purpose {
	case rpc.AgentCompatCapabilityTerminal, rpc.AgentCompatCapabilityFileManager:
		if resourceID != 0 {
			return errAgentcompatCapabilityInvalid
		}
	case rpc.AgentCompatCapabilityNAT:
		if resourceID == 0 {
			return errAgentcompatCapabilityInvalid
		}
	default:
		return errAgentcompatCapabilityInvalid
	}
	return nil
}
