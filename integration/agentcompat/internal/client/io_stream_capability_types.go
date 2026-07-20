package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

var (
	ErrInvalidIOStreamCapabilityRequest = errors.New("client: invalid IOStream capability request")
	ErrIOStreamCapabilityUnavailable    = errors.New("client: IOStream capability unavailable")
	ErrIOStreamCapabilityConflict       = errors.New("client: IOStream capability active")
	ErrIOStreamCapabilityCleanup        = errors.New("client: IOStream capability cleanup failed")
)

type IOStreamCapabilityPurpose string

const (
	IOStreamCapabilityPurposeTerminal    IOStreamCapabilityPurpose = "terminal"
	IOStreamCapabilityPurposeFileManager IOStreamCapabilityPurpose = "file_manager"
	IOStreamCapabilityPurposeNAT         IOStreamCapabilityPurpose = "nat"
)

type IOStreamCapability struct {
	value string
}

func ParseIOStreamCapability(value string) (IOStreamCapability, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 {
		return IOStreamCapability{}, ErrInvalidIOStreamCapabilityRequest
	}
	return IOStreamCapability{value: value}, nil
}

func (capability IOStreamCapability) Value() string {
	return capability.value
}

func (capability IOStreamCapability) MarshalJSON() ([]byte, error) {
	if capability.value == "" {
		return nil, ErrInvalidIOStreamCapabilityRequest
	}
	return json.Marshal(capability.value)
}

func (capability *IOStreamCapability) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return ErrInvalidIOStreamCapabilityRequest
	}
	parsed, err := ParseIOStreamCapability(value)
	if err != nil {
		return err
	}
	*capability = parsed
	return nil
}

type IOStreamID struct {
	value string
}

func (streamID IOStreamID) Value() string {
	return streamID.value
}

func (streamID *IOStreamID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil || value == "" {
		return ErrIOStreamCapabilityUnavailable
	}
	streamID.value = value
	return nil
}

type IOStreamCapabilityRegisterRequest struct {
	Purpose    IOStreamCapabilityPurpose `json:"purpose"`
	ServerID   uint64                    `json:"server_id"`
	ResourceID uint64                    `json:"resource_id,omitempty"`
}

type IOStreamCapabilityRegisterResponse struct {
	Capability IOStreamCapability `json:"capability"`
}

type IOStreamCapabilityAccessRequest struct {
	Capability IOStreamCapability        `json:"capability"`
	Purpose    IOStreamCapabilityPurpose `json:"purpose"`
	ServerID   uint64                    `json:"server_id"`
	ResourceID uint64                    `json:"resource_id,omitempty"`
}

type IOStreamCapabilityWaitRequest IOStreamCapabilityAccessRequest

type IOStreamCapabilityWaitResponse struct {
	StreamID IOStreamID `json:"stream_id"`
}

type ioStreamCapabilityEmptyResponse struct{}

func validateIOStreamCapabilityIdentity(purpose IOStreamCapabilityPurpose, serverID, resourceID uint64) error {
	if serverID == 0 {
		return ErrInvalidIOStreamCapabilityRequest
	}
	switch purpose {
	case IOStreamCapabilityPurposeTerminal, IOStreamCapabilityPurposeFileManager:
		if resourceID != 0 {
			return ErrInvalidIOStreamCapabilityRequest
		}
	case IOStreamCapabilityPurposeNAT:
		if resourceID == 0 {
			return ErrInvalidIOStreamCapabilityRequest
		}
	default:
		return ErrInvalidIOStreamCapabilityRequest
	}
	return nil
}

func validateIOStreamCapabilityAccess(request IOStreamCapabilityAccessRequest) error {
	if request.Capability.value == "" {
		return ErrInvalidIOStreamCapabilityRequest
	}
	return validateIOStreamCapabilityIdentity(request.Purpose, request.ServerID, request.ResourceID)
}
