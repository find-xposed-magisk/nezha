package controller

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

// createAPIToken 是「旧 mcp:* → 新 nezha:*」唯一的归一化入口：
//   - mcp:fs:read / mcp:server:read 归一化为 nezha:server:read；
//   - mcp:server:exec 归一化为 nezha:server:exec；
//   - mcp:fs:write / mcp:fs:delete / mcp:* 不再可签发——它们历史上覆盖范围
//     比 nezha:server:write/delete 窄（只跑 MCP fs 工具），静默映射会扩权。
//
// 这样老调用方传旧 scope 还能创建只读 PAT，但拿不到 write/delete 提权。

func TestCreateAPIToken_RewritesLegacyReadScopeToNezhaRead(t *testing.T) {
	defer setupAPITokenTest(t)()
	c := ctxAsUser(10, model.RoleMember)
	bindJSON(c, model.APITokenCreateRequest{
		Name:   "legacy-reader",
		Scopes: []string{"mcp:fs:read"},
	})
	res, err := createAPIToken(c)
	require.NoError(t, err, "legacy mcp:fs:read must be accepted at create time and rewritten")
	require.Equal(t, []string{model.ScopeServerRead}, res.Scopes,
		"create response must reflect the new unified scope name, not the legacy alias")
}

func TestCreateAPIToken_RewritesLegacyExecScopeToNezhaExec(t *testing.T) {
	defer setupAPITokenTest(t)()
	c := ctxAsUser(10, model.RoleMember)
	bindJSON(c, model.APITokenCreateRequest{
		Name:   "legacy-exec",
		Scopes: []string{"mcp:server:exec"},
	})
	res, err := createAPIToken(c)
	require.NoError(t, err)
	require.Equal(t, []string{model.ScopeServerExec}, res.Scopes)
}

func TestCreateAPIToken_RejectsLegacyMCPWriteScope(t *testing.T) {
	defer setupAPITokenTest(t)()
	c := ctxAsUser(10, model.RoleMember)
	bindJSON(c, model.APITokenCreateRequest{
		Name:   "legacy-writer",
		Scopes: []string{"mcp:fs:write"},
	})
	_, err := createAPIToken(c)
	require.Error(t, err,
		"mcp:fs:write must be rejected: silently mapping to nezha:server:write would expand the original "+
			"MCP-only write capability to every REST server mutation route")
}

func TestCreateAPIToken_RejectsLegacyMCPDeleteScope(t *testing.T) {
	defer setupAPITokenTest(t)()
	c := ctxAsUser(10, model.RoleMember)
	bindJSON(c, model.APITokenCreateRequest{
		Name:   "legacy-deleter",
		Scopes: []string{"mcp:fs:delete"},
	})
	_, err := createAPIToken(c)
	require.Error(t, err)
}

func TestCreateAPIToken_RejectsLegacyMCPWildcardScope(t *testing.T) {
	defer setupAPITokenTest(t)()
	c := ctxAsUser(1, model.RoleAdmin)
	bindJSON(c, model.APITokenCreateRequest{
		Name:   "legacy-admin",
		Scopes: []string{"mcp:*"},
	})
	_, err := createAPIToken(c)
	require.Error(t, err,
		"mcp:* must be rejected even for admin: the new unified namespace is nezha:* / nezha:admin:*")
}
