//go:build linux && agentcompat

package scenario

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
	"github.com/stretchr/testify/require"
)

func TestNAT_HTTPRequestObservesPeerWriteCloseAfterDeclaredBody(t *testing.T) {
	// Given
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()
		request, readErr := http.ReadRequest(bufio.NewReader(connection))
		if readErr != nil {
			serverDone <- readErr
			return
		}
		_, readErr = io.Copy(io.Discard, request.Body)
		if closeErr := request.Body.Close(); readErr == nil {
			readErr = closeErr
		}
		if readErr != nil {
			serverDone <- readErr
			return
		}
		body := "closed"
		if _, writeErr := fmt.Fprintf(connection, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s%s", len(body), body, io.EOF.Error()); writeErr != nil {
			serverDone <- writeErr
			return
		}
		tcpConnection, ok := connection.(*net.TCPConn)
		if !ok {
			serverDone <- errors.New("test listener did not accept TCP connection")
			return
		}
		serverDone <- tcpConnection.CloseWrite()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// When
	response, err := natHTTPRequest(ctx, natHTTPRequestSpec{Endpoint: listener.Addr().String(), Host: natTestDomain, Method: http.MethodGet, Path: "/"})

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.Status)
	require.Equal(t, "closed", response.Body)
	require.Equal(t, io.EOF.Error(), response.LegacyCloseMarker)
	require.True(t, response.PeerWriteClosed)
	require.NoError(t, <-serverDone)
}

func TestNAT_ParseResponsePreservesExactRequestFields(t *testing.T) {
	// Given
	body := natExpectedBody(natHTTPRequestSpec{Host: natTestDomain, Method: http.MethodPatch, Path: "/nat?case=ordinary", Body: "ordinary"})

	// When
	record, err := parseNATResponse(body)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.MethodPatch, record.Method)
	require.Equal(t, "/nat?case=ordinary", record.Path)
	require.Equal(t, natTestDomain, record.Host)
	require.Equal(t, "fixture", record.HeaderValue)
	require.Equal(t, []byte("ordinary"), record.Body)
}

func TestNAT_ParseResponseRejectsMissingEvidence(t *testing.T) {
	// Given
	body := "method=GET\npath=/\n"

	// When
	_, err := parseNATResponse(body)

	// Then
	require.Error(t, err)
}

func TestNAT_ExactRequestObservedRejectsMismatchedHalfCloseEvidence(t *testing.T) {
	// Given
	request := natHTTPRequestSpec{Host: natHalfCloseTestDomain, Method: http.MethodPost, Path: "/nat?case=half-close", Body: "half-closed"}
	exact := fixture.NATEchoRecord{Method: request.Method, Path: request.Path, Host: request.Host, HeaderValue: natEchoHeaderValue, Body: []byte(request.Body)}
	mismatched := exact
	mismatched.Host = natTestDomain

	// When
	observed := natExactRequestObserved(request, exact, mismatched)

	// Then
	require.False(t, observed)
}

func TestNAT_DeletedRouteObservedRequiresFallbackStatusAndBody(t *testing.T) {
	// Given
	request := natHTTPRequestSpec{Host: natTestDomain, Method: http.MethodGet, Path: "/"}

	// When
	observed := natDeletedRouteObserved(natRawResponse{Status: http.StatusOK, Body: "dashboard fallback"}, nil, request)
	echoObserved := natDeletedRouteObserved(natRawResponse{Status: http.StatusOK, Body: natExpectedBody(request)}, nil, request)
	rejectedObserved := natDeletedRouteObserved(natRawResponse{Status: http.StatusNotFound, Body: "not found"}, nil, request)

	// Then
	require.True(t, observed)
	require.False(t, echoObserved)
	require.False(t, rejectedObserved)
}

func TestNAT_FinishReturnsTypedFailedAssertion(t *testing.T) {
	// Given
	assertions := NewAssertionSet()
	assertions.Record("deleted profile no longer routes", false, "backend connection observed")

	// When
	result, err := finishNAT(assertions, nil)

	// Then
	require.EqualError(t, err, "deleted profile no longer routes: backend connection observed")
	require.Equal(t, "nat", result.Name)
	require.False(t, result.Passed)
	require.False(t, result.CleanupOK)
}

func TestNAT_FinishPreservesRuntimeError(t *testing.T) {
	// Given
	runtimeErr := errors.New("NAT runtime failed")

	// When
	result, err := finishNAT(NewAssertionSet(), runtimeErr)

	// Then
	require.ErrorIs(t, err, runtimeErr)
	require.Equal(t, "NAT runtime failed", result.Error)
}
