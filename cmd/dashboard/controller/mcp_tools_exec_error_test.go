package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

// execErrorStream replies every Task with a TaskResult whose Successful=true
// but whose Data carries a model.ExecResult{Error: ...}. This is exactly what
// the real agent does for "agent disabled command execution" / "cmd required"
// / pre-start failures.
type execErrorStream struct {
	errMsg string
}

func (s *execErrorStream) Send(t *pb.Task) error {
	go func(taskID uint64) {
		b, _ := json.Marshal(model.ExecResult{ExitCode: -1, Error: s.errMsg})
		rpc.DeliverMCPResultForTest(&pb.TaskResult{
			Id:         taskID,
			Type:       model.TaskTypeExec,
			Successful: true,
			Data:       string(b),
		})
	}(t.GetId())
	return nil
}

func (s *execErrorStream) Recv() (*pb.TaskResult, error) { return nil, context.Canceled }
func (s *execErrorStream) SetHeader(metadata.MD) error   { return nil }
func (s *execErrorStream) SendHeader(metadata.MD) error  { return nil }
func (s *execErrorStream) SetTrailer(metadata.MD)        {}
func (s *execErrorStream) Context() context.Context      { return context.Background() }
func (s *execErrorStream) SendMsg(any) error             { return nil }
func (s *execErrorStream) RecvMsg(any) error             { return context.Canceled }

// TestServerExec_AgentReportedErrorBecomesToolError pins the protocol contract
// that fs.* tools already obey: when the agent returns a structured result
// with a non-empty Error field, MCP tools/call must surface isError=true and
// audit must record agent_error — not MCPOutcomeOK with a quietly-failed
// structuredContent. The previous handler ignored ExecResult.Error and
// returned res, nil, which made the LLM and the audit log both believe the
// command succeeded while the agent had actually refused it.
func TestServerExec_AgentReportedErrorBecomesToolError(t *testing.T) {
	cleanup, uid := setupMCPTest(t)
	defer cleanup()

	srv, _ := singleton.ServerShared.Get(7)
	srv.SetTaskStream(&execErrorStream{errMsg: "agent disabled command execution"})

	tok, _ := mkToken(t, uid, []string{model.ScopeServerExec}, nil)

	c, w := mcpCallCtx(t, tok, uid, jsonRPCRequest{
		JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: jsonObj(t, toolCallParams{
			Name: "server.exec",
			Arguments: jsonRaw(map[string]any{
				"server_id":       7,
				"cmd":             "echo",
				"timeout_seconds": 2,
			}),
		}),
	})
	mcpEndpoint(c)
	_, tcr := decodeRPC(w)
	require.NotNil(t, tcr, "tools/call must return a tool result envelope")
	require.True(t, tcr.IsError,
		"agent ExecResult.Error must propagate as MCP tool error; got %+v", tcr)
	require.Contains(t, tcr.Content[0].Text, "agent disabled command execution",
		"tool error text must surface the agent-reported error message")
	var result model.ExecResult
	structuredJSON, err := json.Marshal(tcr.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(structuredJSON, &result))
	require.Equal(t, -1, result.ExitCode)
	require.Equal(t, "agent disabled command execution", result.Error)

	require.Eventually(t, func() bool {
		var got model.MCPAuditLog
		err := singleton.DB.Where("token_id = ?", tok.ID).First(&got).Error
		if err != nil {
			return false
		}
		return got.Outcome == model.MCPOutcomeAgentError
	}, 2*time.Second, 20*time.Millisecond,
		"audit row must record agent_error, not ok, when ExecResult.Error is set")
}
