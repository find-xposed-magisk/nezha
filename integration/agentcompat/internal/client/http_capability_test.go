package client

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
	"github.com/stretchr/testify/require"
)

func TestRESTTypedCapabilityHeaderOnlyAttachesWhenRequested(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	capability, err := ParseIOStreamCapability(raw)
	require.NoError(t, err)
	seen := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Get(agentcompatcontract.IOStreamCapabilityHeader)
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte(`{"success":true,"data":{}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	transport := newTestClient(t, Config{BaseURL: server.URL, BearerToken: "private-pat"})

	_, err = DoREST[struct{}, struct{}](context.Background(), transport, RESTRequest[struct{}]{Method: http.MethodPost, Path: "/ordinary"})
	require.NoError(t, err)
	_, err = DoREST[struct{}, struct{}](context.Background(), transport, RESTRequest[struct{}]{Method: http.MethodPost, Path: "/create", IOStreamCapability: capability})
	require.NoError(t, err)
	require.Empty(t, <-seen)
	require.Equal(t, raw, <-seen)
}
