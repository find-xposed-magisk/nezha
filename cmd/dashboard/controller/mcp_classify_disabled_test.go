package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/rpc"
)

// classifyToolError 必须把 rpc.ErrMCPDisabled 归类成 forbidden 类 outcome，而不是
// 当作 agent_error。
//
// ErrMCPDisabled 是 dashboard 主动按下 kill switch 的语义信号（见
// service/rpc/mcp_rpc.go 注释），controller 把它揉进 agent_error 等于把“管理员
// 关了 MCP”和“agent 真出故障”混在一起：审计日志、SIEM 告警和 MCP 客户端的
// structuredContent.error_code 都会错配。
func TestClassifyToolError_MCPDisabledMapsToForbidden(t *testing.T) {
	code, msg := classifyToolError(rpc.ErrMCPDisabled)

	assert.Equal(t, model.MCPOutcomeMCPDisabled, code,
		"rpc.ErrMCPDisabled must map to MCPOutcomeMCPDisabled, not agent_error")
	assert.NotEqual(t, model.MCPOutcomeAgentError, code,
		"kill-switch errors must not be reported as agent_error in audit/SIEM")
	assert.Contains(t, msg, "MCP is disabled",
		"error text should preserve the kill switch reason")
}
