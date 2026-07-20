//go:build linux

package scenario

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/client"
	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
)

type heldNATRequest struct {
	connection net.Conn
	result     chan error
	closeOnce  sync.Once
	closeErr   error
}

func startHeldNATRequest(ctx context.Context, endpoint, domain, identity string, capability client.IOStreamCapability) (*heldNATRequest, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, err
	}
	request := &heldNATRequest{connection: connection, result: make(chan error, 1)}
	method := "PATCH"
	path := "/held/" + identity
	body := "held-body-" + identity
	go func() {
		wire := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: %s\r\nX-AgentCompat-Echo: %s\r\n%s: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", method, path, domain, identity, agentcompatcontract.IOStreamCapabilityHeader, capability.Value(), len(body), body)
		_, requestErr := io.WriteString(connection, wire)
		if requestErr == nil {
			response, readErr := http.ReadResponse(bufio.NewReader(connection), nil)
			if readErr == nil {
				_, readErr = io.Copy(io.Discard, response.Body)
				closeErr := response.Body.Close()
				if readErr == nil {
					readErr = closeErr
				}
			}
			requestErr = readErr
		}
		request.result <- requestErr
	}()
	return request, nil
}

func (request *heldNATRequest) close() error {
	request.closeOnce.Do(func() { request.closeErr = request.connection.Close() })
	return request.closeErr
}
