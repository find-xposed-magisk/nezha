//go:build linux

package scenario

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/fixture"
)

type natRawResponse struct {
	Status            int
	Body              string
	LegacyCloseMarker string
	PeerWriteClosed   bool
}

type natHTTPRequestSpec struct {
	Endpoint string
	Host     string
	Method   string
	Path     string
	Body     string
}

const natEchoHeaderValue = "fixture"

func natHTTPStatus(ctx context.Context, endpoint, host string) (natRawResponse, error) {
	return natHTTPRequest(ctx, natHTTPRequestSpec{Endpoint: endpoint, Host: host, Method: http.MethodGet, Path: "/"})
}

func natHTTPRoundTrip(ctx context.Context, request natHTTPRequestSpec) (natRawResponse, fixture.NATEchoRecord, error) {
	response, err := natHTTPRequest(ctx, request)
	if err != nil {
		return natRawResponse{}, fixture.NATEchoRecord{}, err
	}
	record, err := parseNATResponse(response.Body)
	return response, record, err
}

func natHTTPRequest(ctx context.Context, request natHTTPRequestSpec) (natRawResponse, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", request.Endpoint)
	if err != nil {
		return natRawResponse{}, err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return natRawResponse{}, err
		}
	}
	wireRequest := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: %s\r\nX-AgentCompat-Echo: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", request.Method, request.Path, request.Host, natEchoHeaderValue, len(request.Body), request.Body)
	if _, err := io.WriteString(connection, wireRequest); err != nil {
		return natRawResponse{}, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		return natRawResponse{}, err
	}
	responseBody, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return natRawResponse{}, err
	}
	// Agent preserves its legacy wire contract by forwarding the local read
	// result after the HTTP body. Reading through that marker to EOF proves the
	// backend write-close reached Agent and the stream then terminated.
	closeMarker, peerCloseErr := io.ReadAll(reader)
	if peerCloseErr != nil {
		return natRawResponse{}, fmt.Errorf("observe NAT peer write close: %w", peerCloseErr)
	}
	return natRawResponse{Status: response.StatusCode, Body: string(responseBody), LegacyCloseMarker: string(closeMarker), PeerWriteClosed: true}, nil
}

func natAssertNoBackendConnection(ctx context.Context, backend *fixture.NATEchoBackend) error {
	deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	err := backend.WaitConnection(deadline)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	if err == nil {
		return errors.New("NAT backend received an unexpected connection")
	}
	return err
}

func parseNATResponse(body string) (fixture.NATEchoRecord, error) {
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	for _, key := range []string{"method", "path", "host", "x-agentcompat-echo", "body"} {
		if _, ok := values[key]; !ok {
			return fixture.NATEchoRecord{}, fmt.Errorf("NAT response missing %s", key)
		}
	}
	return fixture.NATEchoRecord{Method: values["method"], Path: values["path"], Host: values["host"], HeaderValue: values["x-agentcompat-echo"], Body: []byte(values["body"])}, nil
}

func natExpectedBody(request natHTTPRequestSpec) string {
	return fmt.Sprintf("method=%s\npath=%s\nhost=%s\nx-agentcompat-echo=%s\nbody=%s\n", request.Method, request.Path, request.Host, natEchoHeaderValue, request.Body)
}
