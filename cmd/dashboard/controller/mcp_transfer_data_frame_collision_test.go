package controller

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
)

func TestTransferDownload_DataFrameBeginningWithErrMagicIsNotMisclassified(t *testing.T) {
	collide := append([]byte(nil), model.MCPFsXferMagicErr...)
	collide = append(collide, []byte("xx-real-file-bytes-xx")...)

	ts, tok, cleanup := setupTransferTest(t, xferAgentDownloadSend(collide))
	defer cleanup()

	url := mintDownloadURL(t, ts, tok, "/srv/collide.bin")
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"file content starting with the NZTE magic must not be misread as an agent error; status=%d body=%q",
		resp.StatusCode, string(body))
	require.Equal(t, collide, body,
		"client must receive the raw file bytes byte-for-byte even when they start with NZTE")
}
