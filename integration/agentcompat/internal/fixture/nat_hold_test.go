package fixture

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
)

func TestNATHoldBackendObservesBeforeReleaseAndRespondsAfterRelease(t *testing.T) {
	backend, err := StartNATHoldBackend()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	connection, err := net.Dial("tcp", backend.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = io.WriteString(connection, "PATCH /hold HTTP/1.1\r\nHost: hold.invalid\r\nX-AgentCompat-Echo: exact\r\nContent-Length: 4\r\n\r\nbody")
	if err != nil {
		t.Fatal(err)
	}
	record, err := backend.WaitRequest(context.Background())
	if err != nil || record.Method != "PATCH" || record.Path != "/hold" || record.Host != "hold.invalid" || record.HeaderValue != "exact" || string(record.Body) != "body" {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	select {
	case <-backend.ResponseReleased():
		t.Fatal("response released before Release")
	default:
	}
	if err := backend.Release(); err != nil {
		t.Fatal(err)
	}
	response, err := httpReadResponse(connection)
	if err != nil || response != "method=PATCH\npath=/hold\nhost=hold.invalid\nx-agentcompat-echo=exact\nbody=body\n" {
		t.Fatalf("response=%q err=%v", response, err)
	}
}

func TestNATHoldBackendCloseInterruptsIncompleteRequest(t *testing.T) {
	backend, err := StartNATHoldBackend()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", backend.Address())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "GET /incomplete HTTP/1.1\r\nHost: hold.invalid\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNATHoldBackendCloseIsConcurrentAndRetainsListenerError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closeFailure := errors.New("hold listener close failed")
	backend := newNATHoldBackend(closeErrorListener{Listener: listener, err: closeFailure})
	connection, err := net.Dial("tcp", backend.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "GET /hold HTTP/1.1\r\nHost: hold.invalid\r\n"); err != nil {
		t.Fatal(err)
	}

	const callers = 8
	errorsSeen := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			errorsSeen <- backend.Close()
		}()
	}
	group.Wait()

	for range callers {
		if err := <-errorsSeen; !errors.Is(err, closeFailure) {
			t.Fatalf("Close error=%v, want %v", err, closeFailure)
		}
	}
	if err := backend.Close(); !errors.Is(err, closeFailure) {
		t.Fatalf("repeated Close error=%v, want %v", err, closeFailure)
	}
}

func newNATHoldBackend(listener net.Listener) *NATHoldBackend {
	backend := &NATHoldBackend{listener: listener, results: make(chan natHoldResult, 1), done: make(chan struct{}), released: make(chan struct{}), observed: make(chan struct{})}
	backend.waitGroup.Add(2)
	go backend.accept()
	return backend
}

func httpReadResponse(connection net.Conn) (string, error) {
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); err == nil {
		err = closeErr
	}
	return string(body), err
}
