package rpc

import (
	"strings"
	"sync/atomic"
	"testing"

	pb "github.com/nezhahq/nezha/proto"
)

// 把"测试 helper 的注释与运行时语义"钉成测试，避免 helper 文档骗读者：
//
//  1. DeliverMCPResultForTest 是显式的"信任路径 / 不做 reporter 校验"入口。
//  2. DeliverMCPResultFromReporterForTest 是带 reporter 校验的入口；
//     reporterID == 0 视为"未知 reporter"，必须被拒绝，不能像旧注释暗示的
//     那样当作"未知/不校验"放行。
//
// 这条契约决定了任何安全敏感的跨包测试调用方式：要绕过 reporter，
// 必须用 DeliverMCPResultForTest，而不是 reporterID=0 通过 reporter 入口。
func TestDeliverMCPResultFromReporterForTest_ZeroReporterIDIsRejected(t *testing.T) {
	taskID := allocateMCPTaskID()
	resultCh := make(chan *pb.TaskResult, 1)
	cancelCh := make(chan struct{})
	mcpInflight.Store(taskID, &mcpInflightEntry{
		serverID:  7,
		result:    resultCh,
		cancel:    cancelCh,
		cancelled: new(atomic.Bool),
	})
	t.Cleanup(func() { mcpInflight.Delete(taskID) })

	DeliverMCPResultFromReporterForTest(&pb.TaskResult{Id: taskID, Data: "x", Successful: true}, 0)

	select {
	case <-resultCh:
		t.Fatalf("reporterID==0 must be rejected by the reporter-checked helper; expected no delivery")
	default:
	}
}

// 同时把"测试 helper 自身的文档约束"钉到代码里：注释必须明确说出
// "reporterID == 0 视为未知 reporter 并被拒绝"，否则未来维护者很容易看着
// "不校验"的旧措辞写出绕过 reporter 的安全敏感测试。
func TestDeliverMCPResultFromReporterForTest_DocStatesZeroIsRejected(t *testing.T) {
	src := mustReadFile(t, "mcp_rpc.go")
	if !strings.Contains(src, "DeliverMCPResultFromReporterForTest") {
		t.Fatalf("expected helper to live in mcp_rpc.go")
	}
	// 提取 helper 上方的注释块：从 helper 名字往上找到第一段连续的 // 行。
	idx := strings.Index(src, "func DeliverMCPResultFromReporterForTest(")
	if idx < 0 {
		t.Fatalf("helper not found in source")
	}
	prefix := src[:idx]
	if !strings.Contains(prefix, "reporterID == 0") {
		t.Fatalf("doc must mention reporterID == 0 contract explicitly")
	}
	if strings.Contains(prefix, "不校验") {
		t.Fatalf("doc still claims reporterID==0 is 不校验; this contradicts deliverMCPResultFromReporter which drops it")
	}
}
