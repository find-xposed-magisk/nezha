package fixture

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFixture_NATEchoHalfClose(t *testing.T) {
	// Given
	backend, err := StartNATHalfCloseEchoBackend()
	requireNoFixtureError(t, err)
	t.Cleanup(func() { requireNoFixtureError(t, backend.Close()) })
	connection, err := net.DialTimeout("tcp", backend.Address(), time.Second)
	requireNoFixtureError(t, err)
	tcpConnection := connection.(*net.TCPConn)
	defer tcpConnection.Close()
	requireNoFixtureError(t, tcpConnection.SetDeadline(time.Now().Add(2*time.Second)))
	requestBody := "half-closed"
	request := fmt.Sprintf("POST /echo?case=half-close HTTP/1.1\r\nHost: nat.invalid\r\nX-AgentCompat-Echo: fixture\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(requestBody), requestBody)

	// When
	_, err = io.WriteString(tcpConnection, request)
	requireNoFixtureError(t, err)
	requireNoFixtureError(t, tcpConnection.CloseWrite())
	response, err := http.ReadResponse(bufio.NewReader(tcpConnection), nil)
	requireNoFixtureError(t, err)
	responseBody, err := io.ReadAll(response.Body)
	requireNoFixtureError(t, err)
	requireNoFixtureError(t, response.Body.Close())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	record, err := backend.WaitRequest(ctx)
	requireNoFixtureError(t, err)

	// Then
	const expected = "method=POST\npath=/echo?case=half-close\nhost=nat.invalid\nx-agentcompat-echo=fixture\nbody=half-closed\n"
	if response.StatusCode != http.StatusOK || string(responseBody) != expected {
		t.Fatalf("NAT echo response = %d %q", response.StatusCode, responseBody)
	}
	if !record.RequestHalfClosed {
		t.Fatal("backend responded before observing request half-close")
	}
	if record.Method != "POST" || record.Path != "/echo?case=half-close" || record.Host != "nat.invalid" || record.HeaderValue != "fixture" || string(record.Body) != requestBody {
		t.Fatalf("NAT request record = %+v", record)
	}
}

func TestFixture_NATEcho(t *testing.T) {
	// Given
	backend, err := StartNATEchoBackend()
	requireNoFixtureError(t, err)
	t.Cleanup(func() { requireNoFixtureError(t, backend.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+backend.Address()+"/echo?case=ordinary", strings.NewReader("ordinary"))
	requireNoFixtureError(t, err)
	request.Host = "nat.invalid"
	request.Header.Set("X-AgentCompat-Echo", "fixture")

	// When
	response, err := http.DefaultClient.Do(request)
	requireNoFixtureError(t, err)
	responseBody, err := io.ReadAll(response.Body)
	requireNoFixtureError(t, err)
	requireNoFixtureError(t, response.Body.Close())
	record, err := backend.WaitRequest(ctx)
	requireNoFixtureError(t, err)

	// Then
	const expected = "method=PUT\npath=/echo?case=ordinary\nhost=nat.invalid\nx-agentcompat-echo=fixture\nbody=ordinary\n"
	if response.StatusCode != http.StatusOK || string(responseBody) != expected {
		t.Fatalf("NAT echo response = %d %q", response.StatusCode, responseBody)
	}
	if record.RequestHalfClosed || record.ResponseHalfClosed || record.Method != http.MethodPut || record.Host != "nat.invalid" {
		t.Fatalf("NAT request record = %+v", record)
	}
}

func TestFixture_NATResponseHalfCloseEcho(t *testing.T) {
	// Given
	backend, err := StartNATResponseHalfCloseEchoBackend()
	requireNoFixtureError(t, err)
	t.Cleanup(func() { requireNoFixtureError(t, backend.Close()) })
	connection, err := net.DialTimeout("tcp", backend.Address(), time.Second)
	requireNoFixtureError(t, err)
	defer connection.Close()
	requireNoFixtureError(t, connection.SetDeadline(time.Now().Add(2*time.Second)))

	// When
	_, err = io.WriteString(connection, "GET /response-half-close HTTP/1.1\r\nHost: nat.invalid\r\nContent-Length: 0\r\n\r\n")
	requireNoFixtureError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	requireNoFixtureError(t, err)
	_, err = io.ReadAll(response.Body)
	requireNoFixtureError(t, err)
	requireNoFixtureError(t, response.Body.Close())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	record, err := backend.WaitRequest(ctx)
	requireNoFixtureError(t, err)

	// Then
	if !record.ResponseHalfClosed || record.RequestHalfClosed {
		t.Fatalf("NAT response half-close record = %+v", record)
	}
}

func TestFixture_NATEchoCloseInterruptsIncompleteRequest(t *testing.T) {
	// Given
	backend, err := StartNATEchoBackend()
	requireNoFixtureError(t, err)
	connection, err := net.DialTimeout("tcp", backend.Address(), time.Second)
	requireNoFixtureError(t, err)
	defer connection.Close()
	_, err = io.WriteString(connection, "GET /incomplete HTTP/1.1\r\nHost: nat.invalid\r\n")
	requireNoFixtureError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	requireNoFixtureError(t, backend.WaitConnection(ctx))

	// When
	closed := make(chan error, 1)
	go func() { closed <- backend.Close() }()

	// Then
	select {
	case err := <-closed:
		requireNoFixtureError(t, err)
	case <-ctx.Done():
		t.Fatal("NAT echo close did not interrupt incomplete request")
	}
}

func TestFixture_NATEchoCloseTerminatesSockets(t *testing.T) {
	// Given
	backend, err := StartNATHalfCloseEchoBackend()
	requireNoFixtureError(t, err)
	address := backend.Address()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	requireNoFixtureError(t, err)
	requireNoFixtureError(t, connection.SetDeadline(time.Now().Add(time.Second)))

	// When
	requireNoFixtureError(t, backend.Close())

	// Then
	buffer := make([]byte, 1)
	if _, err := connection.Read(buffer); err == nil {
		t.Fatal("active NAT socket remained readable after backend close")
	}
	requireNoFixtureError(t, connection.Close())
	if connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
		_ = connection.Close()
		t.Fatal("NAT listener accepted a connection after backend close")
	}
}

func TestFixture_NATEchoCloseIsConcurrentAndIdempotent(t *testing.T) {
	// Given
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	requireNoFixtureError(t, err)
	closeFailure := errors.New("injected listener close failure")
	backend := newNATEchoBackend(closeErrorListener{Listener: listener, err: closeFailure}, false, false)
	connection, err := net.DialTimeout("tcp", backend.Address(), time.Second)
	requireNoFixtureError(t, err)
	defer connection.Close()
	_, err = io.WriteString(connection, "GET /incomplete HTTP/1.1\r\nHost: nat.invalid\r\n")
	requireNoFixtureError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	requireNoFixtureError(t, backend.WaitConnection(ctx))

	// When
	const callers = 16
	start := make(chan struct{})
	closeErrors := make(chan error, callers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for range callers {
		go func() {
			defer waitGroup.Done()
			<-start
			closeErrors <- backend.Close()
		}()
	}
	close(start)
	waitGroup.Wait()
	close(closeErrors)

	// Then
	for closeErr := range closeErrors {
		if !errors.Is(closeErr, closeFailure) {
			t.Fatalf("concurrent close error = %v, want %v", closeErr, closeFailure)
		}
	}
	if closeErr := backend.Close(); !errors.Is(closeErr, closeFailure) {
		t.Fatalf("repeated close error = %v, want %v", closeErr, closeFailure)
	}
}

type closeErrorListener struct {
	net.Listener
	err error
}

func (listener closeErrorListener) Close() error {
	_ = listener.Listener.Close()
	return listener.err
}

func (listener closeErrorListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return alreadyClosedErrorConn{Conn: connection}, nil
}

type alreadyClosedErrorConn struct{ net.Conn }

func (connection alreadyClosedErrorConn) Close() error {
	_ = connection.Conn.Close()
	return net.ErrClosed
}
