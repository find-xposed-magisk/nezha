package model

import (
	"slices"
	"testing"
)

// 这些测试约束「scope 命名统一」契约：
//   - 只有 nezha:* 一套是 first-class scope；
//   - mcp:* 不再作为 HasScope 的别名（避免 mcp:fs:write 静默扩到 REST 的 nezha:server:write）；
//   - AllScopes / AdminOnlyScopes 不再包含 mcp:*，新建 token 不能再签发它们。
//
// 旧 mcp:* 兼容由 createAPIToken 入口做一次性归一化（mcp:fs:read 等只读/exec 映射到
// 对应的 nezha:* read/exec），但 write/delete 类不再映射；详见 controller.createAPIToken。

func TestAllScopes_DoesNotExposeLegacyMCPScopes(t *testing.T) {
	legacy := []string{
		"mcp:*",
		"mcp:server:read",
		"mcp:server:exec",
		"mcp:fs:read",
		"mcp:fs:write",
		"mcp:fs:delete",
	}
	for _, s := range legacy {
		if slices.Contains(AllScopes, s) {
			t.Errorf("AllScopes must not advertise legacy scope %q; only nezha:* is first-class", s)
		}
		if slices.Contains(AdminOnlyScopes, s) {
			t.Errorf("AdminOnlyScopes must not advertise legacy scope %q", s)
		}
	}
}

func TestHasScope_LegacyMCPNoLongerAliasesNezhaWrite(t *testing.T) {
	// 旧 token 数据库里残留 mcp:fs:write，绝不允许覆盖 REST 的 nezha:server:write。
	tok := &APIToken{ScopesCSV: "mcp:fs:write"}
	if tok.HasScope(ScopeServerWrite) {
		t.Fatalf("legacy mcp:fs:write must NOT grant nezha:server:write via HasScope; " +
			"REST routes (server/config, server/:id, batch-delete/server) would become reachable")
	}
	if tok.HasScope(ScopeServerDelete) {
		t.Fatalf("legacy mcp:fs:write must NOT grant nezha:server:delete")
	}
}

func TestHasScope_LegacyMCPDeleteNoLongerAliasesNezhaDelete(t *testing.T) {
	tok := &APIToken{ScopesCSV: "mcp:fs:delete"}
	if tok.HasScope(ScopeServerDelete) {
		t.Fatalf("legacy mcp:fs:delete must NOT grant nezha:server:delete via HasScope")
	}
}

func TestHasScope_LegacyMCPAllNoLongerWildcards(t *testing.T) {
	tok := &APIToken{ScopesCSV: "mcp:*"}
	for _, s := range []string{ScopeServerRead, ScopeServerWrite, ScopeServerDelete, ScopeServerExec} {
		if tok.HasScope(s) {
			t.Errorf("legacy mcp:* must not be treated as a nezha:* wildcard; granted %s", s)
		}
	}
}

func TestHasScope_NezhaWildcardStillWorks(t *testing.T) {
	tok := &APIToken{ScopesCSV: ScopeNezhaAll}
	for _, s := range []string{ScopeServerRead, ScopeServerWrite, ScopeServerDelete, ScopeServerExec} {
		if !tok.HasScope(s) {
			t.Errorf("nezha:* wildcard must still cover %s", s)
		}
	}
}

func TestHasScope_NezhaResourceWildcardStillWorks(t *testing.T) {
	tok := &APIToken{ScopesCSV: "nezha:server:*"}
	for _, s := range []string{ScopeServerRead, ScopeServerWrite, ScopeServerDelete, ScopeServerExec} {
		if !tok.HasScope(s) {
			t.Errorf("nezha:server:* must cover %s", s)
		}
	}
	if tok.HasScope(ScopeServiceRead) {
		t.Fatalf("nezha:server:* must NOT leak into nezha:service:* family")
	}
}
