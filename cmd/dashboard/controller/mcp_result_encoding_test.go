package controller

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

var registerUnmarshalableMCPTool sync.Once

func TestMCPToolCall_unmarshalableSuccessResultReturnsExplicitToolError(t *testing.T) {
	// Given
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	registerUnmarshalableMCPTool.Do(func() {
		registerMCPTool(&mcpTool{
			Name:          "test.unmarshalable-success",
			RequiredScope: "",
			Handler: func(*gin.Context, json.RawMessage) (any, error) {
				return map[string]any{"unsupported": func() {}}, nil
			},
		})
	})
	tok, plainToken := mkToken(t, uid, []string{model.ScopeServerRead}, nil)
	require.NotEmpty(t, plainToken)
	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{Name: "test.unmarshalable-success", Arguments: json.RawMessage("{}")}),
	})

	// When
	mcpEndpoint(c)

	// Then
	_, result := decodeRPC(w)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Nil(t, result.StructuredContent)
	require.Len(t, result.Content, 1)
	require.True(t, strings.Contains(result.Content[0].Text, "encode"), result.Content[0].Text)
}
