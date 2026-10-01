package controller

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestMCPTransferURLPinsUntrustedRequestHostToDashboardHost(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	PurgeTransferEntries()
	t.Cleanup(func() { PurgeTransferEntries() })
	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)

	c, _ := mcpCallCtx(t, tok, uid, nil)
	c.Request.Host = "attacker.example"
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	out, err := mintTransferTool(c, 7, "/srv/file", 60, transferDirDownload, transferEntry{})
	require.NoError(t, err)
	result := out.(map[string]any)
	require.Contains(t, result["url"], "https://example.com/mcp/download/")
	require.NotContains(t, result["url"], "attacker.example")
}

func TestMCPTransferURLRejectsUntrustedHostWithoutCanonicalHost(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()
	PurgeTransferEntries()
	t.Cleanup(func() { PurgeTransferEntries() })
	tok, _ := mkToken(t, uid, []string{model.ScopeServerRead}, nil)
	singleton.Conf.DashboardHost = ""
	singleton.Conf.InstallHost = ""
	singleton.Conf.ListenHost = ""
	singleton.Conf.ReservedHosts = ""

	c, _ := mcpCallCtx(t, tok, uid, nil)
	c.Request = c.Request.Clone(c.Request.Context())
	c.Request.Method = http.MethodPost
	c.Request.Host = "attacker.example"

	_, err := mintTransferTool(c, 7, "/srv/file", 60, transferDirDownload, transferEntry{})
	require.ErrorIs(t, err, errMCPTransferHostNotConfigured)
	entries := 0
	transferEntries.Range(func(_, _ any) bool {
		entries++
		return true
	})
	require.Zero(t, entries, "host validation must happen before a bearer transfer token is minted")
}

func TestMCPTransferURLAllowsOperatorReservedHost(t *testing.T) {
	cleanup, _ := setupMCPTest(t)
	defer cleanup()
	singleton.Conf.DashboardHost = ""
	singleton.Conf.ReservedHosts = "mcp.example.com"

	c, _ := mcpCallCtx(t, nil, 0, nil)
	c.Request.Host = "mcp.example.com"
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	base, err := mcpTransferURLBase(c)
	require.NoError(t, err)
	require.Equal(t, "https://mcp.example.com", base)
}

func TestMCPTransferURLRejectsMalformedConfiguredHost(t *testing.T) {
	cleanup, _ := setupMCPTest(t)
	defer cleanup()
	singleton.Conf.DashboardHost = "https://example.com/path"

	c, _ := mcpCallCtx(t, nil, 0, nil)
	_, err := mcpTransferURLBase(c)
	require.ErrorIs(t, err, errMCPTransferHostNotConfigured)
}
