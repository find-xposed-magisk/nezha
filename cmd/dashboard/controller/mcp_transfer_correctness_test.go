package controller

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/nezhahq/nezha/model"
	pb "github.com/nezhahq/nezha/proto"
	"github.com/nezhahq/nezha/service/rpc"
	"github.com/nezhahq/nezha/service/singleton"
)

// xferAgentSim 模拟 agent 在收到 TaskTypeFsTransfer 后的整个 IOStream 行为：
//   - 通过 net.Pipe 拿到一个 in-memory 全双工流；
//   - 把 dashboard 侧那一端塞进 rpc.NezhaHandlerSingleton.AgentConnected；
//   - 在 agent 侧 goroutine 里跑 upload/download 的协议帧逻辑。
//
// 该函数把"如果是真 agent 会做什么"全部就地展开，使 dashboard 端 transfer
// handler 能在没有真实 gRPC 链路的情况下完整跑过：mint→consume→stream→OK。
type xferStreamMux struct {
	agent func(req *model.FsTransferRequest, dashboardSide io.ReadWriteCloser) ([]byte, error)
}

func (m *xferStreamMux) Send(t *pb.Task) error {
	if t.GetType() != model.TaskTypeFsTransfer {
		return nil
	}
	var req model.FsTransferRequest
	if err := json.Unmarshal([]byte(t.GetData()), &req); err != nil {
		return err
	}
	dashboardSide, agentSide := newFramedPipe()
	if err := rpc.NezhaHandlerSingleton.AgentConnected(req.StreamID, dashboardSide); err != nil {
		return err
	}
	go func() {
		defer agentSide.Close()
		_, _ = m.agent(&req, agentSide)
	}()
	return nil
}

// framedPipe is a frame-preserving full-duplex in-memory stream pair used by
// the MCP transfer tests in place of net.Pipe. Each Write on one side becomes
// exactly one frame on the other side, so RecvFrame on dashboardSide observes
// the same frame boundaries production code sees via grpcx.IOStreamWrapper.
// net.Pipe coalesces bytes and would let an NZTE control frame's bytes spill
// into a previous data frame's parse — the very bug we are testing for.
type framedPipe struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
	once   *sync.Once
	rest   []byte
}

func newFramedPipe() (*framedPipe, *framedPipe) {
	closeCh := make(chan struct{})
	once := new(sync.Once)
	a := make(chan []byte, 64)
	b := make(chan []byte, 64)
	return &framedPipe{in: a, out: b, closed: closeCh, once: once},
		&framedPipe{in: b, out: a, closed: closeCh, once: once}
}

func (p *framedPipe) Write(buf []byte) (int, error) {
	frame := append([]byte(nil), buf...)
	select {
	case p.out <- frame:
		return len(buf), nil
	case <-p.closed:
		return 0, io.ErrClosedPipe
	}
}

func (p *framedPipe) Read(buf []byte) (int, error) {
	if len(p.rest) > 0 {
		n := copy(buf, p.rest)
		p.rest = p.rest[n:]
		return n, nil
	}
	select {
	case frame, ok := <-p.in:
		if !ok {
			return 0, io.EOF
		}
		n := copy(buf, frame)
		if n < len(frame) {
			p.rest = frame[n:]
		}
		return n, nil
	default:
	}
	select {
	case frame, ok := <-p.in:
		if !ok {
			return 0, io.EOF
		}
		n := copy(buf, frame)
		if n < len(frame) {
			p.rest = frame[n:]
		}
		return n, nil
	case <-p.closed:
		return 0, io.EOF
	}
}

func (p *framedPipe) RecvFrame() ([]byte, error) {
	if len(p.rest) > 0 {
		out := p.rest
		p.rest = nil
		return out, nil
	}
	select {
	case frame, ok := <-p.in:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	default:
	}
	select {
	case frame, ok := <-p.in:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	case <-p.closed:
		return nil, io.EOF
	}
}

func (p *framedPipe) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func (m *xferStreamMux) Recv() (*pb.TaskResult, error) { return nil, context.Canceled }
func (m *xferStreamMux) SetHeader(metadata.MD) error   { return nil }
func (m *xferStreamMux) SendHeader(metadata.MD) error  { return nil }
func (m *xferStreamMux) SetTrailer(metadata.MD)        {}
func (m *xferStreamMux) Context() context.Context      { return context.Background() }
func (m *xferStreamMux) SendMsg(any) error             { return nil }
func (m *xferStreamMux) RecvMsg(any) error             { return context.Canceled }

// xferAgentUploadAccept 实现 NZTU + 接收 size 字节 + NZTO 的完整握手。把读到
// 的原始字节作为返回值，方便测试断言。
func xferAgentUploadAccept(req *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
	got, err := xferAgentUploadRead(req, stream)
	if err != nil {
		return got, err
	}
	return got, xferAgentUploadAck(stream, uint64(len(got)))
}

func xferAgentUploadRead(req *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
	header := append([]byte(nil), model.MCPFsXferMagicUploadHdr...)
	sz := make([]byte, 8)
	binary.BigEndian.PutUint64(sz, uint64(req.Size))
	header = append(header, sz...)
	if _, err := stream.Write(header); err != nil {
		return nil, err
	}
	got := make([]byte, 0, req.Size)
	if req.Size > 0 {
		buf := make([]byte, req.Size)
		if _, err := io.ReadFull(stream, buf); err != nil {
			return nil, err
		}
		got = buf
	}
	return got, nil
}

func xferAgentUploadAck(stream io.ReadWriteCloser, size uint64) error {
	ok := append([]byte(nil), model.MCPFsXferMagicOK...)
	finalSize := make([]byte, 8)
	binary.BigEndian.PutUint64(finalSize, size)
	ok = append(ok, finalSize...)
	ok = append(ok, make([]byte, 32)...)
	_, err := stream.Write(ok)
	return err
}

// xferAgentDownloadSend 模拟 agent 向 dashboard 推 payload：发 NZTD、NZTC(chunk)
// 包装的 payload、最后 NZTO。NZTC 包装是 dashboard 区分数据帧与控制帧
// （NZTE/NZTO）的唯一依据；离开它后 dashboard 没办法把首字节恰好等于 NZTE 的
// 合法文件内容与真错误帧区分开。
func xferAgentDownloadSend(payload []byte) func(*model.FsTransferRequest, io.ReadWriteCloser) ([]byte, error) {
	return func(_ *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		hdr := append([]byte(nil), model.MCPFsXferMagicDownloadHdr...)
		sz := make([]byte, 8)
		binary.BigEndian.PutUint64(sz, uint64(len(payload)))
		hdr = append(hdr, sz...)
		hdr = append(hdr, make([]byte, 32)...)
		if _, err := stream.Write(hdr); err != nil {
			return nil, err
		}
		if len(payload) > 0 {
			chunk := append([]byte(nil), model.MCPFsXferMagicChunk...)
			chunkLen := make([]byte, 8)
			binary.BigEndian.PutUint64(chunkLen, uint64(len(payload)))
			chunk = append(chunk, chunkLen...)
			chunk = append(chunk, payload...)
			if _, err := stream.Write(chunk); err != nil {
				return nil, err
			}
		}
		ok := append([]byte(nil), model.MCPFsXferMagicOK...)
		ok = append(ok, sz...)
		ok = append(ok, make([]byte, 32)...)
		_, err := stream.Write(ok)
		return payload, err
	}
}

// xferAgentError 模拟 agent 直接发 NZTE 拒绝。
func xferAgentError(msg string) func(*model.FsTransferRequest, io.ReadWriteCloser) ([]byte, error) {
	return func(_ *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		buf := append([]byte(nil), model.MCPFsXferMagicErr...)
		buf = append(buf, msg...)
		_, err := stream.Write(buf)
		return nil, err
	}
}

func setupTransferTest(t *testing.T, agent func(*model.FsTransferRequest, io.ReadWriteCloser) ([]byte, error)) (*httptest.Server, string, func()) {
	t.Helper()
	cleanupBase, uid := setupMCPTest(t)

	originalHandler := rpc.NezhaHandlerSingleton
	rpc.NezhaHandlerSingleton = rpc.NewNezhaHandler()

	stream := &xferStreamMux{agent: agent}
	srv, _ := singleton.ServerShared.Get(7)
	srv.SetTaskStream(stream)

	_, plain := mkToken(t, uid, []string{
		model.ScopeServerRead, model.ScopeServerWrite,
	}, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/mcp", apiTokenAuthMiddleware(), mcpEndpoint)
	r.GET("/mcp/download/:token", transferDownloadHandler)
	r.POST("/mcp/upload/:token", transferUploadHandler)
	ts := httptest.NewServer(r)
	return ts, plain, func() {
		ts.Close()
		rpc.NezhaHandlerSingleton = originalHandler
		cleanupBase()
	}
}

func mintDownloadURL(t *testing.T, ts *httptest.Server, tok, path string) string {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "fs.download_url",
			"arguments": map[string]any{"server_id": 7, "path": path, "ttl_seconds": 60},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var env map[string]any
	require.NoError(t, json.Unmarshal(out, &env))
	res, _ := env["result"].(map[string]any)
	struc, _ := res["structuredContent"].(map[string]any)
	url, _ := struc["url"].(string)
	require.NotEmpty(t, url, "fs.download_url did not return url: %v", env)
	return ts.URL + url[strings.Index(url, "/mcp/"):]
}

// /mcp/download 必须把 agent 推过来的原始字节一字不差地交给 HTTP 客户端。
func TestTransferDownload_ReturnsRawBinaryBytes(t *testing.T) {
	want := []byte{0x00, 0x01, 0xFF, 0xAB, 'h', 'i'}
	ts, tok, cleanup := setupTransferTest(t, xferAgentDownloadSend(want))
	defer cleanup()

	url := mintDownloadURL(t, ts, tok, "/srv/blob")
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "status=%d body=%q", resp.StatusCode, string(body))
	require.Equal(t, want, body, "client must receive raw file bytes")
}

func mintUploadURL(t *testing.T, ts *httptest.Server, tok, path string) string {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "fs.upload_url",
			"arguments": map[string]any{"server_id": 7, "path": path, "ttl_seconds": 60},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var env map[string]any
	require.NoError(t, json.Unmarshal(out, &env))
	res, _ := env["result"].(map[string]any)
	struc, _ := res["structuredContent"].(map[string]any)
	url, _ := struc["url"].(string)
	require.NotEmpty(t, url, "fs.upload_url did not return url: %v", env)
	return ts.URL + url[strings.Index(url, "/mcp/"):]
}

func TestTransferUpload_PreservesArbitraryBinary(t *testing.T) {
	binary := []byte{0x00, 0x01, 0xC3, 0x28, 0xFF, 0xFE, 'h', 'i'}
	var captured []byte
	var capturedMu sync.Mutex
	agent := func(req *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		got, err := xferAgentUploadRead(req, stream)
		capturedMu.Lock()
		captured = got
		capturedMu.Unlock()
		if err != nil {
			return got, err
		}
		return got, xferAgentUploadAck(stream, uint64(len(got)))
	}
	ts, tok, cleanup := setupTransferTest(t, agent)
	defer cleanup()

	url := mintUploadURL(t, ts, tok, "/srv/upload.bin")
	upResp, err := http.Post(url, "application/octet-stream", bytes.NewReader(binary))
	require.NoError(t, err)
	defer upResp.Body.Close()
	require.Equal(t, http.StatusOK, upResp.StatusCode)

	capturedMu.Lock()
	defer capturedMu.Unlock()
	require.Equal(t, binary, captured, "agent must receive byte-for-byte body")
}

// agent 发完声明的 payload 后没有发任何最终控制帧就关掉 stream 时，
// dashboard 不能把这个未确认的传输当成成功：因为协议规定下载完成由 NZTO
// 帧承载 size/SHA256，缺失最终帧意味着 agent 没有正向确认整段数据。
func TestTransferDownload_MissingFinalOKFrameMustFail(t *testing.T) {
	payload := []byte("partial-but-no-final-ok")
	agent := func(_ *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		hdr := append([]byte(nil), model.MCPFsXferMagicDownloadHdr...)
		sz := make([]byte, 8)
		binary.BigEndian.PutUint64(sz, uint64(len(payload)))
		hdr = append(hdr, sz...)
		hdr = append(hdr, make([]byte, 32)...)
		if _, err := stream.Write(hdr); err != nil {
			return nil, err
		}
		chunk := append([]byte(nil), model.MCPFsXferMagicChunk...)
		chunkLen := make([]byte, 8)
		binary.BigEndian.PutUint64(chunkLen, uint64(len(payload)))
		chunk = append(chunk, chunkLen...)
		chunk = append(chunk, payload...)
		if _, err := stream.Write(chunk); err != nil {
			return nil, err
		}
		// 故意不发任何最终帧：直接由 setup 的 defer agentSide.Close() 关闭。
		return payload, nil
	}

	ts, tok, cleanup := setupTransferTest(t, agent)
	defer cleanup()

	url := mintDownloadURL(t, ts, tok, "/srv/missing-final.bin")
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.NotEqualf(t, http.StatusOK, resp.StatusCode,
		"download without a final NZTO must not be reported as 200 OK; body=%q", string(body))
}

// agent 在 payload 之后写了一个非 NZTO 也非 NZTE 的乱码 4 字节 magic 时，
// dashboard 必须把它当作协议错误，而不是默默成功。
func TestTransferDownload_NonOKNonErrFinalMagicMustFail(t *testing.T) {
	payload := []byte("ok-bytes-but-bogus-tail")
	agent := func(_ *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		hdr := append([]byte(nil), model.MCPFsXferMagicDownloadHdr...)
		sz := make([]byte, 8)
		binary.BigEndian.PutUint64(sz, uint64(len(payload)))
		hdr = append(hdr, sz...)
		hdr = append(hdr, make([]byte, 32)...)
		if _, err := stream.Write(hdr); err != nil {
			return nil, err
		}
		chunk := append([]byte(nil), model.MCPFsXferMagicChunk...)
		chunkLen := make([]byte, 8)
		binary.BigEndian.PutUint64(chunkLen, uint64(len(payload)))
		chunk = append(chunk, chunkLen...)
		chunk = append(chunk, payload...)
		if _, err := stream.Write(chunk); err != nil {
			return nil, err
		}
		// 12 字节、非 NZTO/NZTE 的乱码最终帧。
		bogus := []byte{'X', 'X', 'X', 'X', 0, 0, 0, 0, 0, 0, 0, 0}
		if _, err := stream.Write(bogus); err != nil {
			return nil, err
		}
		return payload, nil
	}

	ts, tok, cleanup := setupTransferTest(t, agent)
	defer cleanup()

	url := mintDownloadURL(t, ts, tok, "/srv/bogus-final.bin")
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.NotEqualf(t, http.StatusOK, resp.StatusCode,
		"download with a non-NZTO non-NZTE final frame must not be reported as 200 OK; body=%q", string(body))
}

// agent 拒绝（NZTE）时 dashboard 必须把错误透出去，不能假装 200。
func TestTransferDownload_SurfacesAgentError(t *testing.T) {
	ts, tok, cleanup := setupTransferTest(t, xferAgentError("file too large"))
	defer cleanup()

	url := mintDownloadURL(t, ts, tok, "/srv/huge")
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.NotEqual(t, http.StatusOK, resp.StatusCode,
		"agent NZTE must surface as non-200 to client")
}

// 下载途中 agent 发现源被截断并切到 NZTE 错误帧时，dashboard 绝不能
// 把那个错误帧的字节当成文件正文塞进 HTTP body —— 协议帧和文件字节
// 共用同一条 IOStream，HTTP 客户端不应收到“200 OK + 截断后混入 NZTE
// magic + agent 错误文本”。
func TestTransferDownload_MidStreamErrorDoesNotCorruptBody(t *testing.T) {
	declared := []byte("HELLO-WORLD!")
	partial := declared[:5]

	agent := func(_ *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		hdr := append([]byte(nil), model.MCPFsXferMagicDownloadHdr...)
		sz := make([]byte, 8)
		binary.BigEndian.PutUint64(sz, uint64(len(declared)))
		hdr = append(hdr, sz...)
		hdr = append(hdr, make([]byte, 32)...)
		if _, err := stream.Write(hdr); err != nil {
			return nil, err
		}
		chunk := append([]byte(nil), model.MCPFsXferMagicChunk...)
		chunkLen := make([]byte, 8)
		binary.BigEndian.PutUint64(chunkLen, uint64(len(partial)))
		chunk = append(chunk, chunkLen...)
		chunk = append(chunk, partial...)
		if _, err := stream.Write(chunk); err != nil {
			return nil, err
		}
		errFrame := append([]byte(nil), model.MCPFsXferMagicErr...)
		errFrame = append(errFrame, []byte("source truncated mid-transfer")...)
		_, err := stream.Write(errFrame)
		return partial, err
	}

	ts, tok, cleanup := setupTransferTest(t, agent)
	defer cleanup()

	url := mintDownloadURL(t, ts, tok, "/srv/blob")
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		require.Failf(t, "mid-stream NZTE leaked into HTTP body",
			"expected non-200 once agent switched to NZTE; got 200 with body=%q (len=%d, declared=%d)",
			string(body), len(body), len(declared))
	}
	require.NotContains(t, string(body), string(model.MCPFsXferMagicErr),
		"NZTE control frame magic must never appear in the HTTP body")
}

// 上传时 Content-Length 超过 100MiB 必须直接 413，不进 IOStream。
func TestTransferUpload_RejectsOversizedBody(t *testing.T) {
	ts, tok, cleanup := setupTransferTest(t, xferAgentUploadAccept)
	defer cleanup()

	url := mintUploadURL(t, ts, tok, "/srv/big.bin")
	body := &bigReader{remaining: model.MCPFsTransferMaxSize + 1}
	req, _ := http.NewRequest("POST", url, body)
	req.ContentLength = int64(model.MCPFsTransferMaxSize + 1)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// dashboard 必须接受 ?sha256=<64hex> 形式并把 32B sha 透传给 agent。这个测试
// 不模拟失败，仅锁定 query 透传 + agent 正常返回 NZTO 时整链路 200。SHA256
// 真不匹配走的是下面 TestTransferUpload_SHA256MismatchReturns502。
func TestTransferUpload_AcceptsSHA256Query(t *testing.T) {
	want := []byte("ohi")
	var sawExpected string
	var sawMu sync.Mutex
	agent := func(req *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		sawMu.Lock()
		sawExpected = req.ExpectedSHA256
		sawMu.Unlock()
		return xferAgentUploadAccept(req, stream)
	}
	ts, tok, cleanup := setupTransferTest(t, agent)
	defer cleanup()

	want64 := strings.Repeat("0", 64)
	url := mintUploadURL(t, ts, tok, "/srv/up.bin")
	url += "?sha256=" + want64
	resp, err := http.Post(url, "application/octet-stream", bytes.NewReader(want))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	sawMu.Lock()
	defer sawMu.Unlock()
	require.Equal(t, want64, sawExpected, "dashboard must forward ?sha256 to agent verbatim")
}

// SHA256 不匹配时 agent 会用 NZTE 拒绝；dashboard 必须把 NZTE 透传成 502 而不是
// 因为 io.CopyN 已经写完 body 就返回 200。原版测试用 xferAgentUploadAccept 模拟
// 成功握手，错误返回值被 dashboard 忽略，最终断言 200，把这条 integrity 错误
// 路径假阳性 pin 住了。此处用 xferAgentError 真正模拟 agent NZTE。
func TestTransferUpload_SHA256MismatchReturns502(t *testing.T) {
	want := []byte("ohi")
	agent := func(req *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		if _, err := xferAgentUploadRead(req, stream); err != nil {
			return nil, err
		}
		return xferAgentError("sha256 mismatch")(req, stream)
	}
	ts, tok, cleanup := setupTransferTest(t, agent)
	defer cleanup()

	url := mintUploadURL(t, ts, tok, "/srv/up.bin")
	url += "?sha256=" + strings.Repeat("0", 64)
	resp, err := http.Post(url, "application/octet-stream", bytes.NewReader(want))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), "sha256 mismatch")
}

type bigReader struct{ remaining int64 }

func (b *bigReader) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > b.remaining {
		n = int(b.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 0
	}
	b.remaining -= int64(n)
	return n, nil
}


