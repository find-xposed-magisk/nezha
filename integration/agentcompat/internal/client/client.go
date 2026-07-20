package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultRequestTimeout   = 10 * time.Second
	defaultTransferTimeout  = 5 * time.Minute
	defaultMaxResponseBytes = int64(8 << 20)
	defaultMaxTransferBytes = int64(100 << 20)
)

var ErrInvalidConfig = errors.New("client: invalid configuration")

type Config struct {
	BaseURL          string
	HTTPClient       *http.Client
	WebSocketDialer  *websocket.Dialer
	BearerToken      string
	Origin           string
	RequestTimeout   time.Duration
	TransferTimeout  time.Duration
	MaxResponseBytes int64
	MaxTransferBytes int64
}

type Client struct {
	baseURL          *url.URL
	httpClient       *http.Client
	webSocketDialer  *websocket.Dialer
	bearerToken      string
	origin           string
	requestTimeout   time.Duration
	transferTimeout  time.Duration
	maxResponseBytes int64
	maxTransferBytes int64
	nextRequestID    atomic.Uint64
	requestCount     atomic.Uint64
}

func (client *Client) RequestCount() uint64 {
	return client.requestCount.Load()
}

func New(config Config) (*Client, error) {
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil || baseURL.Host == "" || (baseURL.Scheme != "http" && baseURL.Scheme != "https") {
		return nil, fmt.Errorf("base URL: %w", ErrInvalidConfig)
	}

	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultRequestTimeout
	}
	transferTimeout := config.TransferTimeout
	if transferTimeout == 0 {
		transferTimeout = defaultTransferTimeout
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	maxTransferBytes := config.MaxTransferBytes
	if maxTransferBytes == 0 {
		maxTransferBytes = defaultMaxTransferBytes
	}
	if requestTimeout < 0 || transferTimeout < 0 || maxResponseBytes < 1 || maxTransferBytes < 1 {
		return nil, fmt.Errorf("request limits: %w", ErrInvalidConfig)
	}

	httpClient := &http.Client{}
	if config.HTTPClient != nil {
		clone := *config.HTTPClient
		httpClient = &clone
	}
	if httpClient.Jar == nil {
		jar, jarErr := cookiejar.New(nil)
		if jarErr != nil {
			return nil, fmt.Errorf("cookie jar: %w", jarErr)
		}
		httpClient.Jar = jar
	}
	httpClient.CheckRedirect = rejectRedirect

	dialer := websocket.DefaultDialer
	if config.WebSocketDialer != nil {
		dialer = config.WebSocketDialer
	}
	dialerClone := *dialer
	if dialerClone.HandshakeTimeout == 0 || dialerClone.HandshakeTimeout > requestTimeout {
		dialerClone.HandshakeTimeout = requestTimeout
	}

	origin := strings.TrimSpace(config.Origin)
	if origin == "" {
		origin = baseURL.Scheme + "://" + baseURL.Host
	}
	return &Client{
		baseURL:          baseURL,
		httpClient:       httpClient,
		webSocketDialer:  &dialerClone,
		bearerToken:      strings.TrimSpace(config.BearerToken),
		origin:           origin,
		requestTimeout:   requestTimeout,
		transferTimeout:  transferTimeout,
		maxResponseBytes: maxResponseBytes,
		maxTransferBytes: maxTransferBytes,
	}, nil
}

func rejectRedirect(*http.Request, []*http.Request) error {
	return ErrRedirect
}

func (client *Client) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, client.requestTimeout)
}

func (client *Client) transferContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, client.transferTimeout)
}

func (client *Client) resolvePath(path string) (*url.URL, error) {
	reference, err := url.Parse(path)
	if err != nil || reference.IsAbs() || reference.Host != "" {
		return nil, fmt.Errorf("request path: %w", ErrInvalidConfig)
	}
	return client.baseURL.ResolveReference(reference), nil
}

func (client *Client) applyAuthenticatedHeaders(request *http.Request, includeCSRF bool) {
	if client.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+client.bearerToken)
	}
	if client.origin != "" {
		request.Header.Set("Origin", client.origin)
	}
	if !includeCSRF || client.httpClient.Jar == nil {
		return
	}
	for _, cookie := range client.httpClient.Jar.Cookies(client.baseURL) {
		if cookie.Name == "nz-csrf" {
			request.Header.Set("X-CSRF-Token", cookie.Value)
			return
		}
	}
}
