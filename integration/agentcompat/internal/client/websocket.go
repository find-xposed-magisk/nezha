package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type FrameType string

const (
	FrameText   FrameType = "text"
	FrameBinary FrameType = "binary"
)

var ErrUnsupportedFrame = errors.New("client: unsupported WebSocket frame")

type Frame struct {
	Type    FrameType
	Payload []byte
}

type WebSocketConnection struct {
	connection *websocket.Conn
	timeout    time.Duration
	readLock   sync.Mutex
	writeLock  sync.Mutex
	closeOnce  sync.Once
	// closeDone publishes the first physical close result to every caller.
	closeDone               chan struct{}
	closeError              error
	afterReadMessageForTest func()
}

func (client *Client) DialWebSocket(ctx context.Context, path string) (*WebSocketConnection, error) {
	requestURL, err := client.resolvePath(path)
	if err != nil {
		return nil, err
	}
	switch requestURL.Scheme {
	case "http":
		requestURL.Scheme = "ws"
	case "https":
		requestURL.Scheme = "wss"
	default:
		return nil, fmt.Errorf("WebSocket scheme: %w", ErrInvalidConfig)
	}
	header := make(http.Header)
	if client.bearerToken != "" {
		header.Set("Authorization", "Bearer "+client.bearerToken)
	}
	if client.origin != "" {
		header.Set("Origin", client.origin)
	}
	requestContext, cancel := client.requestContext(ctx)
	defer cancel()
	connection, response, err := client.webSocketDialer.DialContext(requestContext, requestURL.String(), header)
	if err != nil {
		if response != nil && response.Body != nil {
			defer response.Body.Close()
			body, readErr := readBounded(response.Body, client.maxResponseBytes)
			if readErr != nil {
				return nil, fmt.Errorf("read WebSocket handshake failure: %w", readErr)
			}
			return nil, &WebSocketHandshakeError{StatusCode: response.StatusCode, Message: string(body)}
		}
		if requestContext.Err() != nil {
			return nil, fmt.Errorf("dial WebSocket: %w", requestContext.Err())
		}
		return nil, errorsNewRedacted("dial WebSocket", err)
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	connection.SetReadLimit(client.maxResponseBytes)
	return &WebSocketConnection{connection: connection, timeout: client.requestTimeout, closeDone: make(chan struct{})}, nil
}

func (connection *WebSocketConnection) ReadFrame(ctx context.Context) (Frame, error) {
	readContext, cancel := context.WithTimeout(ctx, connection.timeout)
	defer cancel()
	return connection.readFrame(readContext)
}

// ReadFrameUntil reads one frame using only the caller's cancellation and deadline.
func (connection *WebSocketConnection) ReadFrameUntil(ctx context.Context) (Frame, error) {
	return connection.readFrame(ctx)
}

func (connection *WebSocketConnection) readFrame(ctx context.Context) (Frame, error) {
	connection.readLock.Lock()
	defer connection.readLock.Unlock()
	var cancellationState struct {
		sync.Mutex
		completed bool
	}
	stopCancellation := context.AfterFunc(ctx, func() {
		cancellationState.Lock()
		defer cancellationState.Unlock()
		if !cancellationState.completed {
			_ = connection.Close()
		}
	})
	defer func() {
		cancellationState.Lock()
		cancellationState.completed = true
		cancellationState.Unlock()
		stopCancellation()
	}()
	cancellationOccurred := func() bool {
		cancellationState.Lock()
		defer cancellationState.Unlock()
		if ctx.Err() == nil {
			return false
		}
		cancellationState.completed = true
		return true
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.connection.SetReadDeadline(deadline); err != nil {
			// A cancellation callback may close the socket while SetReadDeadline runs.
			if cancellationOccurred() {
				return Frame{}, fmt.Errorf("read WebSocket frame: %w", ctx.Err())
			}
			return Frame{}, fmt.Errorf("set WebSocket read deadline: %w", err)
		}
	} else if err := connection.connection.SetReadDeadline(time.Time{}); err != nil {
		// Gorilla retains prior deadlines until explicitly cleared.
		if cancellationOccurred() {
			return Frame{}, fmt.Errorf("read WebSocket frame: %w", ctx.Err())
		}
		return Frame{}, fmt.Errorf("set WebSocket read deadline: %w", err)
	}
	messageType, payload, err := connection.connection.ReadMessage()
	if connection.afterReadMessageForTest != nil {
		connection.afterReadMessageForTest()
	}
	cancellationState.Lock()
	cancellationWon := ctx.Err() != nil || !stopCancellation()
	if cancellationWon {
		_ = connection.Close()
	}
	cancellationState.completed = true
	cancellationState.Unlock()
	if cancellationWon {
		return Frame{}, fmt.Errorf("read WebSocket frame: %w", ctx.Err())
	}
	if err != nil {
		if errors.Is(err, websocket.ErrReadLimit) {
			return Frame{}, ErrResponseTooLarge
		}
		var closeError *websocket.CloseError
		if errors.As(err, &closeError) {
			return Frame{}, &WebSocketCloseError{Code: closeError.Code, Text: closeError.Text}
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return Frame{}, fmt.Errorf("read WebSocket frame: %w", context.DeadlineExceeded)
		}
		return Frame{}, errorsNewRedacted("read WebSocket frame", err)
	}
	switch messageType {
	case websocket.TextMessage:
		return Frame{Type: FrameText, Payload: payload}, nil
	case websocket.BinaryMessage:
		return Frame{Type: FrameBinary, Payload: payload}, nil
	default:
		return Frame{}, ErrUnsupportedFrame
	}
}

func (connection *WebSocketConnection) WriteFrame(ctx context.Context, frame Frame) error {
	connection.writeLock.Lock()
	defer connection.writeLock.Unlock()
	writeContext, cancel := context.WithTimeout(ctx, connection.timeout)
	defer cancel()
	stopCancellation := context.AfterFunc(writeContext, func() { _ = connection.Close() })
	defer stopCancellation()
	deadline, _ := writeContext.Deadline()
	if err := connection.connection.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set WebSocket write deadline: %w", err)
	}
	var messageType int
	switch frame.Type {
	case FrameText:
		messageType = websocket.TextMessage
	case FrameBinary:
		messageType = websocket.BinaryMessage
	default:
		return ErrUnsupportedFrame
	}
	if err := connection.connection.WriteMessage(messageType, frame.Payload); err != nil {
		if writeContext.Err() != nil {
			return fmt.Errorf("write WebSocket frame: %w", writeContext.Err())
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return fmt.Errorf("write WebSocket frame: %w", context.DeadlineExceeded)
		}
		return errorsNewRedacted("write WebSocket frame", err)
	}
	return nil
}

func (connection *WebSocketConnection) Close() error {
	connection.closeOnce.Do(func() {
		if err := connection.connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			connection.closeError = err
		}
		close(connection.closeDone)
	})
	<-connection.closeDone
	return connection.closeError
}
