package controller

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func mcpEndpointRawCtx(t *testing.T, tok *model.APIToken, raw []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(w)
	require.NotNil(t, engine)
	c.Request = httptest.NewRequest("POST", "/mcp", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(apiTokenCtxKey, tok)
	c.Set(model.CtxKeyAPIToken, tok)
	return c, w
}

func mcpRateLimitTestSetup(t *testing.T, budget int) *model.APIToken {
	t.Helper()
	originalConf := singleton.Conf
	originalLimiter := mcpRateLimiterShared
	t.Cleanup(func() {
		singleton.Conf = originalConf
		mcpRateLimiterShared = originalLimiter
	})
	cfg := &model.Config{}
	cfg.SetMCPEnabled(true)
	singleton.Conf = &singleton.ConfigClass{Config: cfg}
	mcpRateLimiterShared = newMCPRateLimiter(budget, budget)
	return &model.APIToken{ID: 4243, UserID: 1}
}

// A flood of tools/call requests whose params fail to parse must still consume
// the per-token budget. Otherwise a valid PAT bypasses the limiter by always
// sending malformed arguments.
func TestMCPMalformedToolsCallParamsCountsAgainstRateLimit(t *testing.T) {
	tok := mcpRateLimitTestSetup(t, 2)

	raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"not-an-object"}`)

	for i := 0; i < 5; i++ {
		c, w := mcpEndpointRawCtx(t, tok, raw)
		mcpEndpoint(c)
		require.NotEmpty(t, w.Body.Bytes())
	}

	if mcpRateLimiterShared.Allow(tok.ID) {
		t.Fatal("malformed tools/call params must still consume the rate budget; limiter not saturated")
	}
}

// A flood of unparseable JSON-RPC envelopes from an authenticated PAT must also
// consume the budget.
func TestMCPMalformedEnvelopeCountsAgainstRateLimit(t *testing.T) {
	tok := mcpRateLimitTestSetup(t, 2)

	raw := []byte(`{not valid json`)

	for i := 0; i < 5; i++ {
		c, w := mcpEndpointRawCtx(t, tok, raw)
		mcpEndpoint(c)
		require.NotEmpty(t, w.Body.Bytes())
	}

	if mcpRateLimiterShared.Allow(tok.ID) {
		t.Fatal("malformed JSON-RPC envelope must still consume the rate budget; limiter not saturated")
	}
}
