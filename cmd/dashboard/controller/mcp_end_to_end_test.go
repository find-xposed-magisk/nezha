package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

type e2eStream struct {
	mu       sync.Mutex
	dispatch func(*pb.Task) *pb.TaskResult
}

func (s *e2eStream) Send(t *pb.Task) error {
	// fs.upload_url / fs.download_url 走 IOStream 路径，由独立 mux 处理；
	// 这里的 RPC-style dispatch 只覆盖 fs.read/fs.write/fs.list/fs.delete/server.exec。
	if t.GetType() == model.TaskTypeFsTransfer {
		return e2eHandleFsTransfer(t)
	}
	s.mu.Lock()
	d := s.dispatch
	s.mu.Unlock()
	if d == nil {
		return nil
	}
	go func(task *pb.Task) {
		if res := d(task); res != nil {
			rpc.DeliverMCPResultForTest(res)
		}
	}(t)
	return nil
}

// e2eHandleFsTransfer 模拟真实 agent 收到 TaskTypeFsTransfer：把本地文件
// 系统作为后端，按 op 跑完整协议帧并复制字节。和真 agent 不同：
//   - 不做 sha256 强校验（测试侧用 NZTO 中的 32 字节固定 0 占位）。
//   - 复用 net.Pipe + rpc.NezhaHandlerSingleton.AgentConnected 注入 dashboard 端。
func e2eHandleFsTransfer(t *pb.Task) error {
	var req model.FsTransferRequest
	if err := json.Unmarshal([]byte(t.GetData()), &req); err != nil {
		return err
	}
	dashboardSide, agentSide := net.Pipe()
	if err := rpc.NezhaHandlerSingleton.AgentConnected(req.StreamID, dashboardSide); err != nil {
		return err
	}
	go func() {
		defer agentSide.Close()
		switch req.Op {
		case model.MCPFsTransferOpDownload:
			data, err := os.ReadFile(req.Path)
			if err != nil {
				buf := append([]byte(nil), model.MCPFsXferMagicErr...)
				buf = append(buf, err.Error()...)
				_, _ = agentSide.Write(buf)
				return
			}
			hdr := append([]byte(nil), model.MCPFsXferMagicDownloadHdr...)
			sz := make([]byte, 8)
			binary.BigEndian.PutUint64(sz, uint64(len(data)))
			hdr = append(hdr, sz...)
			hdr = append(hdr, make([]byte, 32)...)
			if _, err := agentSide.Write(hdr); err != nil {
				return
			}
			if len(data) > 0 {
				chunk := append([]byte(nil), model.MCPFsXferMagicChunk...)
				chunkLen := make([]byte, 8)
				binary.BigEndian.PutUint64(chunkLen, uint64(len(data)))
				chunk = append(chunk, chunkLen...)
				chunk = append(chunk, data...)
				if _, err := agentSide.Write(chunk); err != nil {
					return
				}
			}
			ok := append([]byte(nil), model.MCPFsXferMagicOK...)
			ok = append(ok, sz...)
			ok = append(ok, make([]byte, 32)...)
			_, _ = agentSide.Write(ok)
		case model.MCPFsTransferOpUpload:
			hdr := append([]byte(nil), model.MCPFsXferMagicUploadHdr...)
			sz := make([]byte, 8)
			binary.BigEndian.PutUint64(sz, uint64(req.Size))
			hdr = append(hdr, sz...)
			if _, err := agentSide.Write(hdr); err != nil {
				return
			}
			buf := make([]byte, req.Size)
			if req.Size > 0 {
				if _, err := io.ReadFull(agentSide, buf); err != nil {
					return
				}
			}
			if err := os.WriteFile(req.Path, buf, 0o644); err != nil {
				errBuf := append([]byte(nil), model.MCPFsXferMagicErr...)
				errBuf = append(errBuf, err.Error()...)
				_, _ = agentSide.Write(errBuf)
				return
			}
			ok := append([]byte(nil), model.MCPFsXferMagicOK...)
			okSz := make([]byte, 8)
			binary.BigEndian.PutUint64(okSz, uint64(len(buf)))
			ok = append(ok, okSz...)
			ok = append(ok, make([]byte, 32)...)
			_, _ = agentSide.Write(ok)
		}
	}()
	return nil
}

func (s *e2eStream) Recv() (*pb.TaskResult, error) { return nil, context.Canceled }
func (s *e2eStream) SetHeader(metadata.MD) error   { return nil }
func (s *e2eStream) SendHeader(metadata.MD) error  { return nil }
func (s *e2eStream) SetTrailer(metadata.MD)        {}
func (s *e2eStream) Context() context.Context      { return context.Background() }
func (s *e2eStream) SendMsg(any) error             { return nil }
func (s *e2eStream) RecvMsg(any) error             { return context.Canceled }

func agentSim(task *pb.Task) *pb.TaskResult {
	res := &pb.TaskResult{Id: task.GetId(), Type: task.GetType(), Successful: true}
	switch task.GetType() {
	case model.TaskTypeFsList:
		var req model.FsListRequest
		_ = json.Unmarshal([]byte(task.GetData()), &req)
		entries, err := os.ReadDir(req.Path)
		if err != nil {
			b, _ := json.Marshal(model.FsListResult{Error: err.Error()})
			res.Data = string(b)
			return res
		}
		out := make([]model.FsEntry, 0, len(entries))
		for _, e := range entries {
			info, _ := e.Info()
			out = append(out, model.FsEntry{Name: e.Name(), Type: "file", Size: info.Size()})
		}
		b, _ := json.Marshal(model.FsListResult{Entries: out, Total: len(out)})
		res.Data = string(b)
	case model.TaskTypeFsRead:
		var req model.FsReadRequest
		_ = json.Unmarshal([]byte(task.GetData()), &req)
		data, err := os.ReadFile(req.Path)
		if err != nil {
			b, _ := json.Marshal(model.FsReadResult{Error: err.Error()})
			res.Data = string(b)
			return res
		}
		encoding := req.Encoding
		if encoding == "" {
			encoding = "utf8"
		}
		var content string
		switch encoding {
		case "base64":
			content = base64.StdEncoding.EncodeToString(data)
		default:
			content = string(data)
		}
		b, _ := json.Marshal(model.FsReadResult{Content: content, Encoding: encoding, Size: int64(len(data))})
		res.Data = string(b)
	case model.TaskTypeFsWrite:
		var req model.FsWriteRequest
		_ = json.Unmarshal([]byte(task.GetData()), &req)
		data := []byte(req.Content)
		if req.Encoding == "base64" {
			decoded, decErr := base64.StdEncoding.DecodeString(req.Content)
			if decErr != nil {
				b, _ := json.Marshal(model.FsWriteResult{Error: decErr.Error()})
				res.Data = string(b)
				return res
			}
			data = decoded
		}
		_ = os.WriteFile(req.Path, data, 0o644)
		b, _ := json.Marshal(model.FsWriteResult{Size: int64(len(data))})
		res.Data = string(b)
	case model.TaskTypeFsDelete:
		var req model.FsDeleteRequest
		_ = json.Unmarshal([]byte(task.GetData()), &req)
		_ = os.RemoveAll(req.Path)
		b, _ := json.Marshal(model.FsDeleteResult{DeletedCount: 1})
		res.Data = string(b)
	case model.TaskTypeExec:
		b, _ := json.Marshal(model.ExecResult{ExitCode: 0, Stdout: "simulated"})
		res.Data = string(b)
	default:
		res.Successful = false
		res.Data = "unsupported task"
	}
	return res
}

func setupEndToEnd(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	cleanupBase, uid := setupMCPTest(t)

	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()

	stream := &e2eStream{dispatch: agentSim}
	srv, _ := singleton.ServerShared.Get(7)
	srv.SetTaskStream(stream)

	prevCleanup := cleanupBase
	cleanupBase = func() {
		rpc.NezhaHandlerSingleton = originalHandler
		prevCleanup()
	}

	_, plain := mkToken(t, uid, []string{
		model.ScopeInventoryRead,
		model.ScopeInventoryDelete,
		model.ScopeServerRead,
		model.ScopeServerExec,
		model.ScopeServerWrite,
		model.ScopeServerDelete,
	}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/mcp", apiTokenAuthMiddleware(), mcpEndpoint)
	r.GET("/mcp/download/:token", transferDownloadHandler)
	r.POST("/mcp/upload/:token", transferUploadHandler)
	ts := httptest.NewServer(r)

	return ts, plain, func() {
		ts.Close()
		cleanupBase()
	}
}

func e2eCall(t *testing.T, ts *httptest.Server, token, method, toolName string, args any) map[string]any {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if method == "tools/call" {
		argsRaw, _ := json.Marshal(args)
		body["params"] = map[string]any{"name": toolName, "arguments": json.RawMessage(argsRaw)}
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var env map[string]any
	require.NoError(t, json.Unmarshal(out, &env))
	return env
}

func TestE2E_Initialize(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()
	env := e2eCall(t, ts, tok, "initialize", "", nil)
	require.Nil(t, env["error"])
	info := env["result"].(map[string]any)["serverInfo"].(map[string]any)
	require.Equal(t, "nezha-mcp", info["name"])
}

func TestE2E_ToolsList(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()
	env := e2eCall(t, ts, tok, "tools/list", "", nil)
	require.Nil(t, env["error"])
	tools := env["result"].(map[string]any)["tools"].([]any)
	require.GreaterOrEqual(t, len(tools), 9)
}

func TestE2E_WhoamiAndServerList(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()

	env := e2eCall(t, ts, tok, "tools/call", "meta.whoami", map[string]any{})
	res := env["result"].(map[string]any)
	require.False(t, res["isError"] == true)

	env = e2eCall(t, ts, tok, "tools/call", "server.list", map[string]any{})
	res = env["result"].(map[string]any)
	require.False(t, res["isError"] == true)
}

func TestE2E_ServerExec(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()
	env := e2eCall(t, ts, tok, "tools/call", "server.exec", map[string]any{
		"server_id": 7, "cmd": "echo",
	})
	res := env["result"].(map[string]any)
	require.False(t, res["isError"] == true, "exec failed: %v", res)
	struc := res["structuredContent"].(map[string]any)
	require.Equal(t, "simulated", struc["stdout"])
}

func TestE2E_FsLifecycle(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()
	dir := t.TempDir()
	p := filepath.Join(dir, "e2e.txt")

	env := e2eCall(t, ts, tok, "tools/call", "fs.write", map[string]any{
		"server_id": 7, "path": p, "content": "ohi", "encoding": "utf8",
	})
	require.False(t, env["result"].(map[string]any)["isError"] == true)

	env = e2eCall(t, ts, tok, "tools/call", "fs.read", map[string]any{"server_id": 7, "path": p})
	res := env["result"].(map[string]any)
	struc := res["structuredContent"].(map[string]any)
	require.Equal(t, "ohi", struc["content"])

	env = e2eCall(t, ts, tok, "tools/call", "fs.delete", map[string]any{"server_id": 7, "path": p})
	require.False(t, env["result"].(map[string]any)["isError"] == true)
}

func TestE2E_DownloadUploadURL(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()
	dir := t.TempDir()
	p := filepath.Join(dir, "blob.txt")
	require.NoError(t, os.WriteFile(p, []byte("payload"), 0o644))

	env := e2eCall(t, ts, tok, "tools/call", "fs.download_url", map[string]any{
		"server_id": 7, "path": p, "ttl_seconds": 60,
	})
	res := env["result"].(map[string]any)
	require.False(t, res["isError"] == true, "download_url failed: %v", res)
	url := res["structuredContent"].(map[string]any)["url"].(string)
	url = ts.URL + url[strings.Index(url, "/mcp/"):]
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, "payload", string(body))

	upPath := filepath.Join(dir, "up.txt")
	env = e2eCall(t, ts, tok, "tools/call", "fs.upload_url", map[string]any{
		"server_id": 7, "path": upPath, "ttl_seconds": 60,
	})
	res = env["result"].(map[string]any)
	require.False(t, res["isError"] == true)
	upURL := res["structuredContent"].(map[string]any)["url"].(string)
	upURL = ts.URL + upURL[strings.Index(upURL, "/mcp/"):]

	upReq, _ := http.NewRequest("POST", upURL, bytes.NewReader([]byte("hello-upload")))
	upResp, err := http.DefaultClient.Do(upReq)
	require.NoError(t, err)
	defer upResp.Body.Close()
	require.Equal(t, 200, upResp.StatusCode)
	got, _ := os.ReadFile(upPath)
	require.Equal(t, "hello-upload", string(got))
}

// TestE2E_DownloadUploadURL_100MiB 走完整 mint→IOStream→relay 路径，验证
// 大文件能跨越旧 4MiB gRPC 上限，并且字节序保持不变。
func TestE2E_DownloadUploadURL_100MiB(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")

	want := make([]byte, model.MCPFsTransferMaxSize)
	for i := range want {
		want[i] = byte(i % 251)
	}
	require.NoError(t, os.WriteFile(src, want, 0o644))

	env := e2eCall(t, ts, tok, "tools/call", "fs.download_url", map[string]any{
		"server_id": 7, "path": src, "ttl_seconds": 60,
	})
	res := env["result"].(map[string]any)
	require.False(t, res["isError"] == true, "download_url failed: %v", res)
	url := res["structuredContent"].(map[string]any)["url"].(string)
	url = ts.URL + url[strings.Index(url, "/mcp/"):]
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, len(want), len(body), "100MiB body length mismatch")
	require.True(t, bytes.Equal(want, body), "100MiB body content mismatch")

	upPath := filepath.Join(dir, "up.bin")
	env = e2eCall(t, ts, tok, "tools/call", "fs.upload_url", map[string]any{
		"server_id": 7, "path": upPath, "ttl_seconds": 60,
	})
	res = env["result"].(map[string]any)
	require.False(t, res["isError"] == true, "upload_url failed: %v", res)
	upURL := res["structuredContent"].(map[string]any)["url"].(string)
	upURL = ts.URL + upURL[strings.Index(upURL, "/mcp/"):]
	req, _ := http.NewRequest("POST", upURL, bytes.NewReader(want))
	req.ContentLength = int64(len(want))
	upResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer upResp.Body.Close()
	require.Equal(t, 200, upResp.StatusCode)
	got, _ := os.ReadFile(upPath)
	require.Equal(t, len(want), len(got))
	require.True(t, bytes.Equal(want, got))
}

func TestE2E_AuditRowsAreWritten(t *testing.T) {
	ts, tok, cleanup := setupEndToEnd(t)
	defer cleanup()
	_ = e2eCall(t, ts, tok, "tools/call", "meta.whoami", map[string]any{})
	_ = e2eCall(t, ts, tok, "tools/call", "server.list", map[string]any{})

	require.Eventually(t, func() bool {
		var cnt int64
		_ = singleton.DB.Model(&model.MCPAuditLog{}).Count(&cnt).Error
		return cnt >= 2
	}, 3*time.Second, 20*time.Millisecond)
}
