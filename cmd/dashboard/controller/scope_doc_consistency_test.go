package controller

// Pins the human-readable PAT scope table in scope_doc.go to the actual
// REST routes registered in controller.go. Without this, the table drifts
// silently every time a route is added/changed — and the table is the
// source LLM clients (and our frontend SCOPE_OPTIONS copy) read from.
//
// The check is intentionally textual: scope_doc.go is a doc-only file with
// no runtime hooks, and routers() bakes scopes into closures at boot, so
// there is no cheap way to reflect them at test time without an invasive
// refactor. Instead we maintain a single canonical (method, path, scope)
// list here and assert both directions:
//   - every entry appears verbatim in scope_doc.go
//   - every scope-bearing line in scope_doc.go appears in the table
// Adding a new scoped route must update both files, and forgetting either
// is a compile-on-demand failure.

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type scopedRoute struct {
	Method string
	Path   string
	Scope  string
}

func canonicalRoutes() []scopedRoute {
	return []scopedRoute{
		{"GET", "/api/v1/server", "nezha:server:read"},
		{"PATCH", "/api/v1/server/{id}", "nezha:server:write"},
		{"GET", "/api/v1/server/config/{id}", "nezha:server:write"},
		{"POST", "/api/v1/server/config", "nezha:server:write"},
		{"POST", "/api/v1/batch-delete/server", "nezha:server:delete"},
		{"POST", "/api/v1/batch-move/server", "nezha:server:write"},
		{"POST", "/api/v1/force-update/server", "nezha:server:write"},
		{"POST", "/api/v1/server-group", "nezha:server:write"},
		{"PATCH", "/api/v1/server-group/{id}", "nezha:server:write"},
		{"POST", "/api/v1/batch-delete/server-group", "nezha:server:delete"},
		{"POST", "/api/v1/terminal", "nezha:server:exec"},
		{"GET", "/api/v1/ws/terminal/{id}", "nezha:server:exec"},
		{"POST", "/api/v1/file", "nezha:server:write"},
		{"GET", "/api/v1/ws/file/{id}", "nezha:server:write"},
		// optional-auth scoped routes（controller.go:91-98）。这些 GET 端点既支持
		// 未登录访客，也接受 PAT；当走 PAT 路径时 restScopeMiddleware 会强制对应的
		// read scope。漏掉这一段会让 scope_doc.go 与实际 router 漂移而测试不报错。
		{"GET", "/api/v1/ws/server", "nezha:server:read"},
		{"GET", "/api/v1/server-group", "nezha:server:read"},
		{"GET", "/api/v1/service", "nezha:service:read"},
		{"GET", "/api/v1/service/server", "nezha:service:read"},
		{"GET", "/api/v1/service/{id}/history", "nezha:service:read"},
		{"GET", "/api/v1/server/{id}/service", "nezha:service:read"},
		{"GET", "/api/v1/server/{id}/metrics", "nezha:server:read"},

		{"GET", "/api/v1/transfer", "nezha:transfer:read"},
		{"POST", "/api/v1/transfer/{id}/cancel", "nezha:transfer:write"},
		{"POST", "/api/v1/transfer/{id}/retry", "nezha:transfer:write"},
		{"GET", "/api/v1/ws/transfer", "nezha:transfer:read"},

		{"GET", "/api/v1/service/list", "nezha:service:read"},
		{"POST", "/api/v1/service", "nezha:service:write"},
		{"PATCH", "/api/v1/service/{id}", "nezha:service:write"},
		{"POST", "/api/v1/batch-delete/service", "nezha:service:delete"},

		{"GET", "/api/v1/alert-rule", "nezha:alertrule:read"},
		{"POST", "/api/v1/alert-rule", "nezha:alertrule:write"},
		{"PATCH", "/api/v1/alert-rule/{id}", "nezha:alertrule:write"},
		{"POST", "/api/v1/batch-delete/alert-rule", "nezha:alertrule:delete"},

		{"GET", "/api/v1/cron", "nezha:cron:read"},
		{"POST", "/api/v1/cron", "nezha:cron:write"},
		{"PATCH", "/api/v1/cron/{id}", "nezha:cron:write"},
		{"POST", "/api/v1/cron/{id}/manual", "nezha:cron:exec"},
		{"POST", "/api/v1/batch-delete/cron", "nezha:cron:delete"},

		{"GET", "/api/v1/ddns", "nezha:ddns:read"},
		{"GET", "/api/v1/ddns/providers", "nezha:ddns:read"},
		{"POST", "/api/v1/ddns", "nezha:ddns:write"},
		{"PATCH", "/api/v1/ddns/{id}", "nezha:ddns:write"},
		{"POST", "/api/v1/batch-delete/ddns", "nezha:ddns:delete"},

		{"GET", "/api/v1/nat", "nezha:nat:read"},
		{"POST", "/api/v1/nat", "nezha:nat:write"},
		{"PATCH", "/api/v1/nat/{id}", "nezha:nat:write"},
		{"POST", "/api/v1/batch-delete/nat", "nezha:nat:delete"},

		{"GET", "/api/v1/notification", "nezha:notification:read"},
		{"POST", "/api/v1/notification", "nezha:notification:write"},
		{"PATCH", "/api/v1/notification/{id}", "nezha:notification:write"},
		{"POST", "/api/v1/batch-delete/notification", "nezha:notification:delete"},

		{"GET", "/api/v1/notification-group", "nezha:notification-group:read"},
		{"POST", "/api/v1/notification-group", "nezha:notification-group:write"},
		{"PATCH", "/api/v1/notification-group/{id}", "nezha:notification-group:write"},
		{"POST", "/api/v1/batch-delete/notification-group", "nezha:notification-group:delete"},

		{"GET", "/api/v1/user", "nezha:admin:*"},
		{"POST", "/api/v1/user", "nezha:admin:*"},
		{"POST", "/api/v1/batch-delete/user", "nezha:admin:*"},
		{"GET", "/api/v1/waf", "nezha:admin:*"},
		{"POST", "/api/v1/batch-delete/waf", "nezha:admin:*"},
		{"GET", "/api/v1/online-user", "nezha:admin:*"},
		{"POST", "/api/v1/online-user/batch-block", "nezha:admin:*"},
		{"PATCH", "/api/v1/setting", "nezha:admin:*"},
		{"POST", "/api/v1/maintenance", "nezha:admin:*"},
	}
}

var scopeDocLineRE = regexp.MustCompile(`^(GET|POST|PATCH|DELETE|PUT)\s+(/api/v1/\S+)\s+(nezha:\S+)$`)

func extractScopeDocEntries(t *testing.T) map[string]scopedRoute {
	t.Helper()
	raw, err := os.ReadFile("scope_doc.go")
	require.NoError(t, err)
	entries := map[string]scopedRoute{}
	for _, line := range strings.Split(string(raw), "\n") {
		stripped := strings.TrimPrefix(line, "//")
		stripped = strings.TrimSpace(stripped)
		stripped = strings.Join(strings.Fields(stripped), " ")
		m := scopeDocLineRE.FindStringSubmatch(stripped)
		if m == nil {
			continue
		}
		r := scopedRoute{Method: m[1], Path: m[2], Scope: m[3]}
		entries[r.Method+" "+r.Path] = r
	}
	return entries
}

func TestScopeDocMatchesCanonicalRoutes(t *testing.T) {
	doc := extractScopeDocEntries(t)
	for _, want := range canonicalRoutes() {
		key := want.Method + " " + want.Path
		got, ok := doc[key]
		if !ok {
			t.Errorf("scope_doc.go missing entry: %s %s (expected scope %s)", want.Method, want.Path, want.Scope)
			continue
		}
		if got.Scope != want.Scope {
			t.Errorf("scope_doc.go scope mismatch for %s %s: doc=%s code=%s", want.Method, want.Path, got.Scope, want.Scope)
		}
	}
}

func TestCanonicalRoutesCoverScopeDoc(t *testing.T) {
	doc := extractScopeDocEntries(t)
	canonical := map[string]scopedRoute{}
	for _, r := range canonicalRoutes() {
		canonical[r.Method+" "+r.Path] = r
	}
	for key, entry := range doc {
		if _, ok := canonical[key]; !ok {
			t.Errorf("scope_doc.go has %s %s (scope %s) with no canonical route — stale doc or missing test entry", entry.Method, entry.Path, entry.Scope)
		}
	}
}
