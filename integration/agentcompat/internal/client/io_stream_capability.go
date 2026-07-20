package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

const (
	ioStreamCapabilityRegisterPath   = "/agentcompat/io-stream-capability/register"
	ioStreamCapabilityWaitPath       = "/agentcompat/io-stream-capability/wait"
	ioStreamCapabilityCancelPath     = "/agentcompat/io-stream-capability/cancel"
	ioStreamCapabilityUnregisterPath = "/agentcompat/io-stream-capability/unregister"
)

const (
	ioStreamCapabilityInvalidMessage     = "agentcompat capability request is invalid"
	ioStreamCapabilityUnavailableMessage = "agentcompat capability is not available"
	ioStreamCapabilityConflictMessage    = "agentcompat capability is active"
	ioStreamCapabilityCleanupMessage     = "agentcompat capability cleanup failed"
)

type IOStreamCapabilityClient struct {
	transport *Client
}

func (client *Client) IOStreamCapabilities() IOStreamCapabilityClient {
	return IOStreamCapabilityClient{transport: client}
}

func (client IOStreamCapabilityClient) Register(ctx context.Context, request IOStreamCapabilityRegisterRequest) (IOStreamCapabilityRegisterResponse, error) {
	if err := validateIOStreamCapabilityIdentity(request.Purpose, request.ServerID, request.ResourceID); err != nil {
		return IOStreamCapabilityRegisterResponse{}, err
	}
	response, err := DoREST[IOStreamCapabilityRegisterRequest, IOStreamCapabilityRegisterResponse](ctx, client.transport, RESTRequest[IOStreamCapabilityRegisterRequest]{
		Method: http.MethodPost, Path: ioStreamCapabilityRegisterPath, Body: &request,
	})
	if err != nil {
		return IOStreamCapabilityRegisterResponse{}, mapIOStreamCapabilityError(err)
	}
	if response.Capability.value == "" {
		return IOStreamCapabilityRegisterResponse{}, ErrIOStreamCapabilityUnavailable
	}
	return response, nil
}

func (client IOStreamCapabilityClient) Wait(ctx context.Context, request IOStreamCapabilityWaitRequest) (IOStreamCapabilityWaitResponse, error) {
	access := IOStreamCapabilityAccessRequest(request)
	if err := validateIOStreamCapabilityAccess(access); err != nil {
		return IOStreamCapabilityWaitResponse{}, err
	}
	response, err := DoREST[IOStreamCapabilityWaitRequest, IOStreamCapabilityWaitResponse](ctx, client.transport, RESTRequest[IOStreamCapabilityWaitRequest]{
		Method: http.MethodPost, Path: ioStreamCapabilityWaitPath, Body: &request,
	})
	if err != nil {
		return IOStreamCapabilityWaitResponse{}, mapIOStreamCapabilityError(err)
	}
	if response.StreamID.value == "" {
		return IOStreamCapabilityWaitResponse{}, ErrIOStreamCapabilityUnavailable
	}
	return response, nil
}

func (client IOStreamCapabilityClient) Cancel(ctx context.Context, request IOStreamCapabilityAccessRequest) error {
	if err := validateIOStreamCapabilityAccess(request); err != nil {
		return err
	}
	_, err := DoREST[IOStreamCapabilityAccessRequest, ioStreamCapabilityEmptyResponse](ctx, client.transport, RESTRequest[IOStreamCapabilityAccessRequest]{
		Method: http.MethodPost, Path: ioStreamCapabilityCancelPath, Body: &request,
	})
	return mapIOStreamCapabilityError(err)
}

func (client IOStreamCapabilityClient) Unregister(ctx context.Context, request IOStreamCapabilityAccessRequest) error {
	if err := validateIOStreamCapabilityAccess(request); err != nil {
		return err
	}
	_, err := DoREST[IOStreamCapabilityAccessRequest, ioStreamCapabilityEmptyResponse](ctx, client.transport, RESTRequest[IOStreamCapabilityAccessRequest]{
		Method: http.MethodPost, Path: ioStreamCapabilityUnregisterPath, Body: &request,
	})
	return mapIOStreamCapabilityError(err)
}

func mapIOStreamCapabilityError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, ErrUnauthorized) {
		return ErrUnauthorized
	}
	if errors.Is(err, ErrSemanticFailure) {
		message := err.Error()
		switch {
		case strings.HasSuffix(message, ioStreamCapabilityInvalidMessage):
			return ErrInvalidIOStreamCapabilityRequest
		case strings.HasSuffix(message, ioStreamCapabilityConflictMessage):
			return ErrIOStreamCapabilityConflict
		case strings.HasSuffix(message, ioStreamCapabilityCleanupMessage):
			return ErrIOStreamCapabilityCleanup
		case strings.HasSuffix(message, ioStreamCapabilityUnavailableMessage):
			return ErrIOStreamCapabilityUnavailable
		}
	}
	return ErrIOStreamCapabilityUnavailable
}
