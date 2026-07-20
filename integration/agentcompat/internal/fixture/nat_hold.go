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

type NATHoldBackend struct {
	listener     net.Listener
	results      chan natHoldResult
	done         chan struct{}
	released     chan struct{}
	observed     chan struct{}
	closeOnce    sync.Once
	releaseOnce  sync.Once
	closeErr     error
	waitGroup    sync.WaitGroup
	connectionMu sync.Mutex
	connection   net.Conn
}

type natHoldResult struct {
	record NATEchoRecord
	err    error
}

func StartNATHoldBackend() (*NATHoldBackend, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for NAT hold: %w", err)
	}
	backend := &NATHoldBackend{listener: listener, results: make(chan natHoldResult, 1), done: make(chan struct{}), released: make(chan struct{}), observed: make(chan struct{})}
	backend.waitGroup.Add(2)
	go backend.accept()
	return backend, nil
}

func (backend *NATHoldBackend) Address() string { return backend.listener.Addr().String() }

func (backend *NATHoldBackend) RequestObserved() <-chan struct{} { return backend.observed }

func (backend *NATHoldBackend) ResponseReleased() <-chan struct{} { return backend.released }

func (backend *NATHoldBackend) WaitRequest(ctx context.Context) (NATEchoRecord, error) {
	select {
	case result := <-backend.results:
		return result.record, result.err
	case <-ctx.Done():
		return NATEchoRecord{}, ctx.Err()
	case <-backend.done:
		return NATEchoRecord{}, errors.New("NAT hold backend closed")
	}
}

func (backend *NATHoldBackend) Release() error {
	backend.releaseOnce.Do(func() { close(backend.released) })
	return nil
}

func (backend *NATHoldBackend) Close() error {
	backend.closeOnce.Do(func() {
		close(backend.done)
		backend.closeErr = normalizeNATHoldCloseError(backend.listener.Close())
		backend.connectionMu.Lock()
		connection := backend.connection
		backend.connectionMu.Unlock()
		if connection != nil {
			backend.closeErr = errors.Join(backend.closeErr, normalizeNATHoldCloseError(connection.Close()))
		}
		backend.releaseOnce.Do(func() { close(backend.released) })
		backend.waitGroup.Wait()
	})
	return backend.closeErr
}

func normalizeNATHoldCloseError(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (backend *NATHoldBackend) accept() {
	defer backend.waitGroup.Done()
	connection, err := backend.listener.Accept()
	if err != nil {
		backend.waitGroup.Done()
		if !errors.Is(err, net.ErrClosed) {
			backend.publish(natHoldResult{err: fmt.Errorf("accept NAT hold connection: %w", err)})
		}
		return
	}
	backend.connectionMu.Lock()
	backend.connection = connection
	backend.connectionMu.Unlock()
	select {
	case <-backend.done:
		_ = connection.Close()
	default:
	}
	go backend.handle(connection)
}

func (backend *NATHoldBackend) handle(connection net.Conn) {
	defer backend.waitGroup.Done()
	defer func() { _ = connection.Close() }()
	request, err := http.ReadRequest(bufio.NewReader(connection))
	if err != nil {
		backend.publish(natHoldResult{err: fmt.Errorf("read NAT hold request: %w", err)})
		return
	}
	body, err := io.ReadAll(request.Body)
	if closeErr := request.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		backend.publish(natHoldResult{err: fmt.Errorf("read NAT hold body: %w", err)})
		return
	}
	record := NATEchoRecord{Method: request.Method, Path: request.URL.RequestURI(), Host: request.Host, HeaderValue: request.Header.Get("X-AgentCompat-Echo"), Body: append([]byte(nil), body...), SensitiveHeadersPresent: request.Header.Get(agentcompatcontract.IOStreamCapabilityHeader) != "" || request.Header.Get("Authorization") != ""}
	backend.publish(natHoldResult{record: record})
	close(backend.observed)
	select {
	case <-backend.released:
		responseBody := fmt.Sprintf("method=%s\npath=%s\nhost=%s\nx-agentcompat-echo=%s\nbody=%s\n", record.Method, record.Path, record.Host, record.HeaderValue, record.Body)
		response := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(responseBody), responseBody)
		if _, err := io.WriteString(connection, response); err != nil {
			backend.publish(natHoldResult{err: fmt.Errorf("write NAT hold response: %w", err)})
		}
	case <-backend.done:
	}
}

func (backend *NATHoldBackend) publish(result natHoldResult) {
	select {
	case backend.results <- result:
	case <-backend.done:
	}
}
