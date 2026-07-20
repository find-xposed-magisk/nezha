package controller

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func mcpEndpointTestCtx(t *testing.T, tok *model.APIToken, body any) (*gin.Context, *httptest.ResponseRecorder, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(w)
	require.NotNil(t, engine)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	c.Request = httptest.NewRequest("POST", "/mcp", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(apiTokenCtxKey, tok)
	c.Set(model.CtxKeyAPIToken, tok)
	return c, w, nil
}

func TestMCPEndpointTestCtx_surfacesJSONMarshalError(t *testing.T) {
	// Given
	tok := &model.APIToken{ID: 4242, UserID: 1}
	unsupportedBody := map[string]any{"unsupported": func() {}}

	// When
	_, _, err := mcpEndpointTestCtx(t, tok, unsupportedBody)

	// Then
	require.Error(t, err)
}

// An unknown tool name in tools/call must still consume the per-token rate
// budget; otherwise a valid PAT can flood /mcp with does-not-exist tools and
// bypass the limiter entirely.
func TestMCPUnknownToolCountsAgainstRateLimit(t *testing.T) {
	originalConf := singleton.Conf
	originalLimiter := mcpRateLimiterShared
	t.Cleanup(func() {
		singleton.Conf = originalConf
		mcpRateLimiterShared = originalLimiter
	})

	cfg := &model.Config{}
	cfg.SetMCPEnabled(true)
	singleton.Conf = &singleton.ConfigClass{Config: cfg}

	mcpRateLimiterShared = newMCPRateLimiter(2, 2)

	tok := &model.APIToken{ID: 4242, UserID: 1}

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "does.not.exist", "arguments": map[string]any{}},
	}

	var lastStatus int
	for i := 0; i < 5; i++ {
		c, w, err := mcpEndpointTestCtx(t, tok, body)
		require.NoError(t, err)
		mcpEndpoint(c)
		lastStatus = w.Code
	}

	if !mcpRateLimiterSaturated(tok.ID) {
		t.Fatalf("after 5 unknown-tool calls with a budget of 2, the limiter must be saturated (last status %d)", lastStatus)
	}
}

func mcpRateLimiterSaturated(tokenID uint64) bool {
	return !mcpRateLimiterShared.Allow(tokenID)
}
