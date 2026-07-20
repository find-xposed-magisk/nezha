package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/nezhahq/nezha/pkg/agentcompatcontract"
)

type CommonResponse[T any] struct {
	Success bool   `json:"success"`
	Data    T      `json:"data"`
	Error   string `json:"error"`
}

type RESTRequest[T any] struct {
	Method             string
	Path               string
	Body               *T
	IOStreamCapability IOStreamCapability
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token  string `json:"token"`
	Expire string `json:"expire"`
}

func (client *Client) Login(ctx context.Context, request LoginRequest) (LoginResponse, error) {
	return REST[LoginRequest, LoginResponse](ctx, client, RESTRequest[LoginRequest]{
		Method: http.MethodPost,
		Path:   "/api/v1/login",
		Body:   &request,
	})
}

func REST[Request, Response any](ctx context.Context, client *Client, request RESTRequest[Request]) (Response, error) {
	var zero Response
	requestURL, err := client.resolvePath(request.Path)
	if err != nil {
		return zero, err
	}

	var body io.Reader
	if request.Body != nil {
		encoded, marshalErr := json.Marshal(request.Body)
		if marshalErr != nil {
			return zero, fmt.Errorf("encode REST request: %w", marshalErr)
		}
		body = bytes.NewReader(encoded)
	}

	requestContext, cancel := client.requestContext(ctx)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, request.Method, requestURL.String(), body)
	if err != nil {
		return zero, fmt.Errorf("create REST request: %w", err)
	}
	if request.Body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	client.applyAuthenticatedHeaders(httpRequest, true)
	if request.IOStreamCapability.Value() != "" {
		httpRequest.Header.Set(agentcompatcontract.IOStreamCapabilityHeader, request.IOStreamCapability.Value())
	}

	status, responseBody, err := client.execute(httpRequest, client.maxResponseBytes)
	if err != nil {
		return zero, err
	}
	if status < 200 || status >= 300 {
		var envelope CommonResponse[Response]
		if json.Unmarshal(responseBody, &envelope) == nil {
			return zero, &HTTPError{StatusCode: status, Message: Redact(envelope.Error)}
		}
		return zero, &HTTPError{StatusCode: status, Message: Redact(string(responseBody))}
	}
	var envelope CommonResponse[Response]
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return zero, fmt.Errorf("decode REST response: %w", err)
	}
	if !envelope.Success {
		return zero, fmt.Errorf("%w: %s", ErrSemanticFailure, Redact(envelope.Error))
	}
	return envelope.Data, nil
}

func DoREST[Request, Response any](ctx context.Context, client *Client, request RESTRequest[Request]) (Response, error) {
	return REST[Request, Response](ctx, client, request)
}

func (client *Client) execute(request *http.Request, maxBytes int64) (int, []byte, error) {
	client.requestCount.Add(1)
	response, err := client.httpClient.Do(request)
	if err != nil {
		if request.Context().Err() != nil {
			return 0, nil, fmt.Errorf("HTTP request: %w", request.Context().Err())
		}
		return 0, nil, errorsNewRedacted("HTTP request", err)
	}
	defer response.Body.Close()

	body, err := readBounded(response.Body, maxBytes)
	if err != nil {
		return response.StatusCode, nil, err
	}
	return response.StatusCode, body, nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func errorsNewRedacted(operation string, err error) error {
	return &redactedOperationError{operation: operation, cause: err}
}

type redactedOperationError struct {
	operation string
	cause     error
}

func (err *redactedOperationError) Error() string {
	return fmt.Sprintf("%s: %s", err.operation, Redact(err.cause.Error()))
}

func (err *redactedOperationError) Unwrap() error {
	return err.cause
}
