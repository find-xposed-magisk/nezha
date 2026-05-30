package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// H7 regression: the MCP endpoint must cap incoming JSON-RPC body size
// BEFORE decoding. Without this, a valid PAT can post a multi-GB body and
// the dashboard exhausts memory in ShouldBindJSON. We assert the body
// reader is wrapped in http.MaxBytesReader; the exact error path the
// decoder takes is irrelevant as long as the cap is enforced.
func TestMCPEndpoint_BodyIsCappedByMaxBytesReader(t *testing.T) {
	prevConf := singleton.Conf
	cfg := &model.Config{}
	cfg.SetMCPEnabled(true)
	singleton.Conf = &singleton.ConfigClass{Config: cfg}
	t.Cleanup(func() { singleton.Conf = prevConf })

	tok := &model.APIToken{ID: 1, ScopesCSV: "nezha:server:read"}
	// 16 MiB of valid-JSON whitespace prefix forces the decoder to actually
	// stream past the limit, exercising MaxBytesReader.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":"` +
		strings.Repeat("x", mcpJSONRPCMaxBodyBytes+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(apiTokenCtxKey, tok)
	c.Set(model.CtxKeyAPIToken, tok)

	mcpEndpoint(c)

	if !strings.Contains(w.Body.String(), "request body") &&
		w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body must be rejected with a body-size error, got code=%d body=%s",
			w.Code, w.Body.String())
	}
}

func TestMCPEndpoint_AcceptsSmallBody(t *testing.T) {
	prevConf := singleton.Conf
	cfg := &model.Config{}
	cfg.SetMCPEnabled(true)
	singleton.Conf = &singleton.ConfigClass{Config: cfg}
	t.Cleanup(func() { singleton.Conf = prevConf })

	tok := &model.APIToken{ID: 1, ScopesCSV: "nezha:server:read"}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set(apiTokenCtxKey, tok)
	c.Set(model.CtxKeyAPIToken, tok)

	mcpEndpoint(c)

	if w.Code != http.StatusOK {
		t.Fatalf("small valid body must succeed, got code=%d body=%s", w.Code, w.Body.String())
	}
}
