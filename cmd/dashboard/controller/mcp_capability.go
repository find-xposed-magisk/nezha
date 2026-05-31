package controller

import (
	"strings"

	"github.com/nezhahq/nezha/model"
)

// MCPMinAgentVersion 是支持 MCP 的最低 agent 版本。
//
// release 流程：在 agent ship 了 MCP handlers 后，把此值更新为该 release 的版本号。
// 不变量：必须为非空。否则旧 agent 收到 TaskTypeExec/TaskTypeFs* 等新任务类型时
// 走 default 分支不回 TaskResult，dashboard 要等 CallAgent 超时（30s）甚至更久
// （fs.transfer 的 IOStream attach 30s）才能感知，这是 server-transfer 已经
// 通过 MinServerTransferAgentVersion 修复过的同类问题。
const MCPMinAgentVersion = "v2.1.0"

// requireAgentSupportsMCP 在 tool handler 调 CallAgent 之前快速失败不支持的 agent。
// 仅作为 UX 优化：真正的安全/正确性由 agent 端 task switch 的 default 分支保障。
func requireAgentSupportsMCP(server *model.Server) error {
	if MCPMinAgentVersion == "" || server == nil || server.Host == nil {
		return nil
	}
	if compareSemver(server.Host.Version, MCPMinAgentVersion) < 0 {
		return errMCPUnsupported
	}
	return nil
}

// compareSemver 比较两个 "MAJOR.MINOR.PATCH[-suffix]" 字符串，返回 -1/0/1。
// semverParts 已剥掉可选的 "v" 前缀与 "-/+" 后缀，所以只按三段数字定序：
// 数字段相等即视为相等版本。绝不能回退到字符串字典序——agent 上报 "2.1.0"
// 而门槛常量是 "v2.1.0"，'2'(0x32) < 'v'(0x76) 会把相等版本误判为更旧，
// 导致所有 agent 被错误地判为不支持 MCP。
func compareSemver(a, b string) int {
	aparts := semverParts(a)
	bparts := semverParts(b)
	for i := 0; i < 3; i++ {
		if aparts[i] < bparts[i] {
			return -1
		}
		if aparts[i] > bparts[i] {
			return 1
		}
	}
	return 0
}

func semverParts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
