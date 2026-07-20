package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIOStreamCapabilityClientUsesTypedPATAuthenticatedWireContract(t *testing.T) {
	rawCapability := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("c", 32)))
	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestNumber++
		require.Equal(t, "Bearer private-pat", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		switch requestNumber {
		case 1:
			require.Equal(t, "/agentcompat/io-stream-capability/register", request.URL.Path)
			var body IOStreamCapabilityRegisterRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			require.Equal(t, IOStreamCapabilityPurposeTerminal, body.Purpose)
			require.Equal(t, uint64(7), body.ServerID)
			require.Zero(t, body.ResourceID)
			_, err := writer.Write([]byte(`{"success":true,"data":{"capability":"` + rawCapability + `"}}`))
			require.NoError(t, err)
		case 2:
			require.Equal(t, "/agentcompat/io-stream-capability/wait", request.URL.Path)
			var body IOStreamCapabilityWaitRequest
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			require.Equal(t, rawCapability, body.Capability.Value())
			_, err := writer.Write([]byte(`{"success":true,"data":{"stream_id":"private-stream"}}`))
			require.NoError(t, err)
		case 3, 4:
			expectedPath := "/agentcompat/io-stream-capability/cancel"
			if requestNumber == 4 {
				expectedPath = "/agentcompat/io-stream-capability/unregister"
			}
			require.Equal(t, expectedPath, request.URL.Path)
			_, err := writer.Write([]byte(`{"success":true,"data":{}}`))
			require.NoError(t, err)
		default:
			t.Fatalf("unexpected request %d", requestNumber)
		}
	}))
	t.Cleanup(server.Close)
	transport := newTestClient(t, Config{BaseURL: server.URL, BearerToken: "private-pat"})
	capabilities := transport.IOStreamCapabilities()

	registered, err := capabilities.Register(context.Background(), IOStreamCapabilityRegisterRequest{Purpose: IOStreamCapabilityPurposeTerminal, ServerID: 7})
	require.NoError(t, err)
	require.Equal(t, rawCapability, registered.Capability.Value())
	waited, err := capabilities.Wait(context.Background(), IOStreamCapabilityWaitRequest{Capability: registered.Capability, Purpose: IOStreamCapabilityPurposeTerminal, ServerID: 7})
	require.NoError(t, err)
	require.Equal(t, "private-stream", waited.StreamID.Value())
	access := IOStreamCapabilityAccessRequest{Capability: registered.Capability, Purpose: IOStreamCapabilityPurposeTerminal, ServerID: 7}
	require.NoError(t, capabilities.Cancel(context.Background(), access))
	require.NoError(t, capabilities.Unregister(context.Background(), access))
	require.Equal(t, 4, requestNumber)
}

func TestIOStreamCapabilityClientValidatesBeforeDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid request must not dispatch")
	}))
	t.Cleanup(server.Close)
	transport := newTestClient(t, Config{BaseURL: server.URL})
	capabilities := transport.IOStreamCapabilities()

	_, err := capabilities.Register(context.Background(), IOStreamCapabilityRegisterRequest{Purpose: "unknown", ServerID: 7})
	require.ErrorIs(t, err, ErrInvalidIOStreamCapabilityRequest)
	_, err = capabilities.Register(context.Background(), IOStreamCapabilityRegisterRequest{Purpose: IOStreamCapabilityPurposeNAT, ServerID: 7})
	require.ErrorIs(t, err, ErrInvalidIOStreamCapabilityRequest)
	_, err = capabilities.Wait(context.Background(), IOStreamCapabilityWaitRequest{Purpose: IOStreamCapabilityPurposeTerminal, ServerID: 7})
	require.ErrorIs(t, err, ErrInvalidIOStreamCapabilityRequest)
	require.Zero(t, transport.RequestCount())
}

func TestIOStreamCapabilityClientErrorsNeverEchoSensitiveValues(t *testing.T) {
	rawCapability := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))
	capability, err := ParseIOStreamCapability(rawCapability)
	require.NoError(t, err)
	privateValues := []string{rawCapability, "private-stream", "private-pat", "Authorization", "private-creator", "private-server"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, writeErr := writer.Write([]byte(`{"success":false,"error":"capability ` + rawCapability + ` stream private-stream Authorization: Bearer private-pat creator private-creator server private-server"}`))
		require.NoError(t, writeErr)
	}))
	t.Cleanup(server.Close)
	transport := newTestClient(t, Config{BaseURL: server.URL, BearerToken: "private-pat"})
	access := IOStreamCapabilityAccessRequest{Capability: capability, Purpose: IOStreamCapabilityPurposeTerminal, ServerID: 7}

	for _, invoke := range []func() error{
		func() error {
			_, callErr := transport.IOStreamCapabilities().Wait(context.Background(), IOStreamCapabilityWaitRequest(access))
			return callErr
		},
		func() error { return transport.IOStreamCapabilities().Cancel(context.Background(), access) },
		func() error { return transport.IOStreamCapabilities().Unregister(context.Background(), access) },
	} {
		callErr := invoke()
		require.ErrorIs(t, callErr, ErrIOStreamCapabilityUnavailable)
		for _, privateValue := range privateValues {
			require.NotContains(t, callErr.Error(), privateValue)
		}
	}
}

func TestIOStreamCapabilityClientPreservesCancellationIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(time.Second):
			t.Error("request context was not canceled")
		}
	}))
	t.Cleanup(server.Close)
	transport := newTestClient(t, Config{BaseURL: server.URL})
	rawCapability := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	capability, err := ParseIOStreamCapability(rawCapability)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = transport.IOStreamCapabilities().Wait(ctx, IOStreamCapabilityWaitRequest{Capability: capability, Purpose: IOStreamCapabilityPurposeTerminal, ServerID: 7})
	require.True(t, errors.Is(err, context.Canceled))
}

func TestIOStreamCapabilityClientMapsTypedNonsecretErrors(t *testing.T) {
	messages := []struct {
		message string
		target  error
	}{
		{message: ioStreamCapabilityConflictMessage, target: ErrIOStreamCapabilityConflict},
		{message: ioStreamCapabilityCleanupMessage, target: ErrIOStreamCapabilityCleanup},
		{message: ioStreamCapabilityUnavailableMessage, target: ErrIOStreamCapabilityUnavailable},
	}
	for _, testCase := range messages {
		t.Run(testCase.message, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, err := writer.Write([]byte(`{"success":false,"error":"` + testCase.message + `"}`))
				require.NoError(t, err)
			}))
			t.Cleanup(server.Close)
			transport := newTestClient(t, Config{BaseURL: server.URL})
			rawCapability := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))
			capability, err := ParseIOStreamCapability(rawCapability)
			require.NoError(t, err)
			callErr := transport.IOStreamCapabilities().Unregister(context.Background(), IOStreamCapabilityAccessRequest{Capability: capability, Purpose: IOStreamCapabilityPurposeTerminal, ServerID: 7})
			require.ErrorIs(t, callErr, testCase.target)
			require.Equal(t, testCase.target.Error(), callErr.Error())
		})
	}
}
