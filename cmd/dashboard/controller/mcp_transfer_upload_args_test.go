package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

// fs.upload_url 必须把 agent 已经支持的上传语义（mode / create_dirs /
// if_match_sha256）从 MCP tool arguments 透传到 agent 的 FsTransferRequest。
// 当前实现复用了 fs.download_url 的 fsDownloadURLArgs，只解析 server_id /
// path / ttl_seconds，导致这些字段被静默丢弃，跨仓 wire model + agent 能力
// 与 MCP 工具调用面失联。
func TestMintFsUploadURL_PropagatesModeCreateDirsAndIfMatchToAgent(t *testing.T) {
	var captured *model.FsTransferRequest
	var mu sync.Mutex
	agent := func(req *model.FsTransferRequest, stream io.ReadWriteCloser) ([]byte, error) {
		mu.Lock()
		copyReq := *req
		captured = &copyReq
		mu.Unlock()
		got, err := xferAgentUploadRead(req, stream)
		if err != nil {
			return got, err
		}
		return got, xferAgentUploadAck(stream, uint64(len(got)))
	}
	ts, tok, cleanup := setupTransferTest(t, agent)
	defer cleanup()

	url := mintFsUploadURLWithOptions(t, ts, tok, "/srv/upload.bin", map[string]any{
		"mode":            "0640",
		"create_dirs":     true,
		"if_match_sha256": strings.Repeat("a", 64),
	})

	upResp, err := http.Post(url, "application/octet-stream", bytes.NewReader([]byte("hello")))
	require.NoError(t, err)
	defer upResp.Body.Close()
	require.Equal(t, http.StatusOK, upResp.StatusCode)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, captured, "agent must have received the FsTransferRequest")
	require.Equal(t, "0640", captured.Mode,
		"fs.upload_url must forward mode to the agent FsTransferRequest")
	require.True(t, captured.CreateDirs,
		"fs.upload_url must forward create_dirs to the agent FsTransferRequest")
	require.Equal(t, strings.Repeat("a", 64), captured.IfMatchSHA256,
		"fs.upload_url must forward if_match_sha256 to the agent FsTransferRequest")
}

func mintFsUploadURLWithOptions(t *testing.T, ts *httptest.Server, tok, path string, extra map[string]any) string {
	t.Helper()
	args := map[string]any{
		"server_id":   7,
		"path":        path,
		"ttl_seconds": 60,
	}
	for k, v := range extra {
		args[k] = v
	}
	body := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "fs.upload_url",
			"arguments": args,
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
	require.NotEmptyf(t, url, "fs.upload_url did not return url: %v", env)
	return ts.URL + url[strings.Index(url, "/mcp/"):]
}
