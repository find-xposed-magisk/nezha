package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolCall[Arguments any] struct {
	Name      string
	Arguments Arguments
}

type ToolCallResult[Result any] struct {
	Content           []MCPContent `json:"content"`
	StructuredContent Result       `json:"structuredContent"`
	IsError           bool         `json:"isError"`
}

type toolCallWireResult struct {
	Content           []MCPContent    `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

type InitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

type jsonRPCRequest[Params any] struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  Params `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      uint64           `json:"id"`
	Result  *json.RawMessage `json:"result"`
	Error   *RPCError        `json:"error"`
}

type toolCallParams[Arguments any] struct {
	Name      string    `json:"name"`
	Arguments Arguments `json:"arguments"`
}

func (client *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	return mcpCall[struct{}, InitializeResult](ctx, client, "initialize", struct{}{})
}

func (client *Client) ListTools(ctx context.Context) (ToolsListResult, error) {
	return mcpCall[struct{}, ToolsListResult](ctx, client, "tools/list", struct{}{})
}

func CallTool[Arguments, Result any](ctx context.Context, client *Client, call ToolCall[Arguments]) (ToolCallResult[Result], error) {
	wireResult, err := mcpCall[toolCallParams[Arguments], toolCallWireResult](ctx, client, "tools/call", toolCallParams[Arguments]{
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	if err != nil {
		return ToolCallResult[Result]{}, err
	}
	if wireResult.IsError {
		message := "tool returned an error"
		if len(wireResult.Content) > 0 && wireResult.Content[0].Text != "" {
			message = wireResult.Content[0].Text
		}
		return ToolCallResult[Result]{}, &ToolFailure{Message: message, StructuredContent: json.RawMessage(Redact(string(wireResult.StructuredContent)))}
	}
	var result Result
	if len(wireResult.StructuredContent) > 0 && string(wireResult.StructuredContent) != "null" {
		if err := json.Unmarshal(wireResult.StructuredContent, &result); err != nil {
			return ToolCallResult[Result]{}, fmt.Errorf("decode MCP tool structured content: %w", err)
		}
	}
	return ToolCallResult[Result]{Content: wireResult.Content, StructuredContent: result, IsError: wireResult.IsError}, nil
}

func mcpCall[Params, Result any](ctx context.Context, client *Client, method string, params Params) (Result, error) {
	var zero Result
	requestID := client.nextRequestID.Add(1)
	requestBody, err := json.Marshal(jsonRPCRequest[Params]{JSONRPC: "2.0", ID: requestID, Method: method, Params: params})
	if err != nil {
		return zero, fmt.Errorf("encode MCP request: %w", err)
	}
	requestURL, err := client.resolvePath("/mcp")
	if err != nil {
		return zero, err
	}
	requestContext, cancel := client.requestContext(ctx)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, requestURL.String(), bytes.NewReader(requestBody))
	if err != nil {
		return zero, fmt.Errorf("create MCP request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	client.applyAuthenticatedHeaders(httpRequest, false)

	status, responseBody, err := client.execute(httpRequest, client.maxResponseBytes)
	if err != nil {
		return zero, err
	}
	var envelope jsonRPCResponse
	if status < 200 || status >= 300 {
		message := ""
		if json.Unmarshal(responseBody, &envelope) == nil && envelope.Error != nil {
			message = Redact(envelope.Error.Message)
		} else {
			message = Redact(string(responseBody))
		}
		return zero, &HTTPError{StatusCode: status, Message: message}
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return zero, fmt.Errorf("decode MCP response: %w", err)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != requestID {
		return zero, fmt.Errorf("%w: invalid response envelope", ErrJSONRPC)
	}
	if (envelope.Result == nil) == (envelope.Error == nil) {
		return zero, fmt.Errorf("%w: response must contain exactly one of result or error", ErrJSONRPC)
	}
	if envelope.Error != nil {
		envelope.Error.Message = Redact(envelope.Error.Message)
		return zero, envelope.Error
	}
	if string(*envelope.Result) == "null" {
		return zero, fmt.Errorf("%w: null result", ErrJSONRPC)
	}
	var result Result
	if err := json.Unmarshal(*envelope.Result, &result); err != nil {
		return zero, fmt.Errorf("decode MCP result: %w", err)
	}
	return result, nil
}
