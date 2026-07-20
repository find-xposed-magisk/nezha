package fixture

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
)

type NATEchoRecord struct {
	Method                  string
	Path                    string
	Host                    string
	HeaderValue             string
	Body                    []byte
	RequestHalfClosed       bool
	ResponseHalfClosed      bool
	SensitiveHeadersPresent bool
}

type natEchoResult struct {
	record NATEchoRecord
	err    error
}

type NATEchoBackend struct {
	listener                net.Listener
	results                 chan natEchoResult
	done                    chan struct{}
	connectionsReady        chan struct{}
	requireRequestHalfClose bool
	halfCloseResponse       bool
	closeOnce               sync.Once
	closeErr                error
	waitGroup               sync.WaitGroup
	mutex                   sync.Mutex
	connections             map[net.Conn]struct{}
	closing                 bool
}

func StartNATEchoBackend() (*NATEchoBackend, error) {
	return startNATEchoBackend(false, false)
}

func StartNATHalfCloseEchoBackend() (*NATEchoBackend, error) {
	return startNATEchoBackend(true, false)
}

func StartNATResponseHalfCloseEchoBackend() (*NATEchoBackend, error) {
	return startNATEchoBackend(false, true)
}

func startNATEchoBackend(requireRequestHalfClose, halfCloseResponse bool) (*NATEchoBackend, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for NAT echo: %w", err)
	}
	return newNATEchoBackend(listener, requireRequestHalfClose, halfCloseResponse), nil
}

func newNATEchoBackend(listener net.Listener, requireRequestHalfClose, halfCloseResponse bool) *NATEchoBackend {
	backend := &NATEchoBackend{
		listener:                listener,
		results:                 make(chan natEchoResult, 16),
		done:                    make(chan struct{}),
		connectionsReady:        make(chan struct{}, 16),
		requireRequestHalfClose: requireRequestHalfClose,
		halfCloseResponse:       halfCloseResponse,
		connections:             make(map[net.Conn]struct{}),
	}
	backend.waitGroup.Add(1)
	go backend.accept()
	return backend
}

func (backend *NATEchoBackend) Address() string {
	return backend.listener.Addr().String()
}

func (backend *NATEchoBackend) WaitRequest(ctx context.Context) (NATEchoRecord, error) {
	select {
	case result := <-backend.results:
		return result.record, result.err
	case <-ctx.Done():
		return NATEchoRecord{}, ctx.Err()
	case <-backend.done:
		return NATEchoRecord{}, errors.New("NAT echo backend closed")
	}
}

func (backend *NATEchoBackend) WaitConnection(ctx context.Context) error {
	select {
	case <-backend.connectionsReady:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for NAT echo connection: %w", ctx.Err())
	case <-backend.done:
		return errors.New("NAT echo backend closed")
	}
}

func (backend *NATEchoBackend) Close() error {
	backend.closeOnce.Do(func() {
		close(backend.done)
		backend.closeErr = normalizeNATEchoCloseError(backend.listener.Close())
		backend.mutex.Lock()
		backend.closing = true
		connections := make([]net.Conn, 0, len(backend.connections))
		for connection := range backend.connections {
			connections = append(connections, connection)
		}
		backend.mutex.Unlock()
		for _, connection := range connections {
			backend.closeErr = errors.Join(backend.closeErr, normalizeNATEchoCloseError(connection.Close()))
		}
		backend.waitGroup.Wait()
	})
	// sync.Once publishes the first shutdown result after cleanup completes, so
	// concurrent and repeated callers observe the same outcome.
	return backend.closeErr
}

func normalizeNATEchoCloseError(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (backend *NATEchoBackend) accept() {
	defer backend.waitGroup.Done()
	for {
		connection, err := backend.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			backend.publish(natEchoResult{err: fmt.Errorf("accept NAT echo connection: %w", err)})
			return
		}
		backend.mutex.Lock()
		if backend.closing {
			backend.mutex.Unlock()
			_ = connection.Close()
			continue
		}
		backend.connections[connection] = struct{}{}
		backend.waitGroup.Add(1)
		backend.mutex.Unlock()
		select {
		case backend.connectionsReady <- struct{}{}:
		default:
		}
		go backend.handle(connection)
	}
}

func (backend *NATEchoBackend) handle(connection net.Conn) {
	defer backend.waitGroup.Done()
	defer func() {
		backend.mutex.Lock()
		delete(backend.connections, connection)
		backend.mutex.Unlock()
		_ = connection.Close()
	}()
	reader := bufio.NewReader(connection)
	request, err := http.ReadRequest(reader)
	if err != nil {
		backend.publish(natEchoResult{err: fmt.Errorf("read NAT echo request: %w", err)})
		return
	}
	body, err := io.ReadAll(request.Body)
	if closeErr := request.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		backend.publish(natEchoResult{err: fmt.Errorf("read NAT echo body: %w", err)})
		return
	}
	halfClosed := false
	if backend.requireRequestHalfClose {
		_, halfCloseErr := reader.ReadByte()
		halfClosed = errors.Is(halfCloseErr, io.EOF)
		if halfCloseErr != nil && !halfClosed {
			backend.publish(natEchoResult{err: fmt.Errorf("observe NAT request half-close: %w", halfCloseErr)})
			return
		}
	}
	record := NATEchoRecord{
		Method:                  request.Method,
		Path:                    request.URL.RequestURI(),
		Host:                    request.Host,
		HeaderValue:             request.Header.Get("X-AgentCompat-Echo"),
		Body:                    append([]byte(nil), body...),
		RequestHalfClosed:       halfClosed,
		SensitiveHeadersPresent: request.Header.Get(agentcompatcontract.IOStreamCapabilityHeader) != "" || request.Header.Get("Authorization") != "",
	}
	responseBody := fmt.Sprintf("method=%s\npath=%s\nhost=%s\nx-agentcompat-echo=%s\nbody=%s\n", record.Method, record.Path, record.Host, record.HeaderValue, record.Body)
	response := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(responseBody), responseBody)
	if _, err := io.WriteString(connection, response); err != nil {
		backend.publish(natEchoResult{err: fmt.Errorf("write NAT echo response: %w", err)})
		return
	}
	if tcpConnection, ok := connection.(*net.TCPConn); ok && backend.halfCloseResponse {
		if err := tcpConnection.CloseWrite(); err != nil {
			backend.publish(natEchoResult{err: fmt.Errorf("half-close NAT echo response: %w", err)})
			return
		}
		record.ResponseHalfClosed = true
	}
	backend.publish(natEchoResult{record: record})
}

func (backend *NATEchoBackend) publish(result natEchoResult) {
	select {
	case backend.results <- result:
	case <-backend.done:
	}
}
