package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

var (
	ErrHTTPStatus       = errors.New("client: HTTP status failure")
	ErrSemanticFailure  = errors.New("client: semantic failure")
	ErrResponseTooLarge = errors.New("client: response too large")
	ErrTransferTooLarge = errors.New("client: transfer too large")
	ErrTransferExpired  = errors.New("client: transfer URL expired")
	ErrUnauthorized     = errors.New("client: unauthorized")
	ErrRedirect         = errors.New("client: redirect rejected")
	ErrJSONRPC          = errors.New("client: JSON-RPC failure")
	ErrToolFailure      = errors.New("client: MCP tool failure")
)

var (
	authorizationPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?)[^\s,;"']+`)
	bearerPattern        = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]+`)
	transferTokenPattern = regexp.MustCompile(`(?i)(/mcp/(?:download|upload)/)[^?\s]+`)
	credentialPattern    = regexp.MustCompile(`(?i)(["']?(?:x-csrf-token|csrf|token|jwt[_-]?(?:secret(?:[_-]?key)?|token)?|pat|api[_-]?(?:key|token)|access[_-]?token|agent[_-]?secret(?:[_-]?key)?|client[_-]?secret|password|credential|signature)["']?\s*[:=]\s*["']?)[^"'\s,;&}]+(["']?)`)
	querySecretPattern   = regexp.MustCompile(`(?i)([?&](?:token|access_token|api_key|jwt|pat|secret|authorization|sig|signature|x-amz-signature)=)[^&#\s]+`)
	jwtPattern           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
)

type HTTPError struct {
	StatusCode int
	Message    string
}

type WebSocketHandshakeError struct {
	StatusCode int
	Message    string
}

func (err *WebSocketHandshakeError) Error() string {
	return fmt.Sprintf("WebSocket handshake: status %d: %s", err.StatusCode, Redact(err.Message))
}

type WebSocketCloseError struct {
	Code int
	Text string
}

func (err *WebSocketCloseError) Error() string {
	return fmt.Sprintf("WebSocket closed: code %d: %s", err.Code, Redact(err.Text))
}

func (err *HTTPError) Error() string {
	if err.Message == "" {
		return fmt.Sprintf("%s: status %d", ErrHTTPStatus, err.StatusCode)
	}
	return fmt.Sprintf("%s: status %d: %s", ErrHTTPStatus, err.StatusCode, Redact(err.Message))
}

func (err *HTTPError) Is(target error) bool {
	return target == ErrHTTPStatus || (target == ErrUnauthorized && err.StatusCode == 401)
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolFailure struct {
	Message           string
	StructuredContent json.RawMessage
}

func (err *ToolFailure) Error() string {
	if err.Message == "" {
		return ErrToolFailure.Error()
	}
	return fmt.Sprintf("%s: %s", ErrToolFailure, Redact(err.Message))
}

func (err *ToolFailure) Is(target error) bool {
	return target == ErrToolFailure
}

func (err *RPCError) Error() string {
	return fmt.Sprintf("%s: code %d: %s", ErrJSONRPC, err.Code, Redact(err.Message))
}

func (err *RPCError) Is(target error) bool {
	return target == ErrJSONRPC || (target == ErrUnauthorized && err.Code == -32001)
}

func Redact(value string) string {
	redacted := authorizationPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	redacted = bearerPattern.ReplaceAllString(redacted, `${1}[REDACTED]`)
	redacted = credentialPattern.ReplaceAllString(redacted, `${1}[REDACTED]${2}`)
	redacted = querySecretPattern.ReplaceAllString(redacted, `${1}[REDACTED]`)
	redacted = jwtPattern.ReplaceAllString(redacted, `[REDACTED]`)
	return transferTokenPattern.ReplaceAllString(redacted, `${1}[REDACTED]`)
}
