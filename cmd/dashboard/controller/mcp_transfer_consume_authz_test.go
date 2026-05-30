package controller

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

func TestTransferConsume_RevokedTokenIsRejected(t *testing.T) {
	ts, tok, cleanup := setupTransferTest(t, xferAgentDownloadSend([]byte("ok")))
	defer cleanup()
	url := mintDownloadURL(t, ts, tok, "/srv/file")

	if err := singleton.DB.Where("token_hash = ?", model.HashAPIToken(tok)).
		Delete(&model.APIToken{}).Error; err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusUnauthorized, resp.StatusCode,
		"download URL must return 401 after the originating PAT is revoked; body=%s", string(body))
}

func TestTransferConsume_NarrowedServerWhitelistIsRejected(t *testing.T) {
	ts, tok, cleanup := setupTransferTest(t, xferAgentDownloadSend([]byte("ok")))
	defer cleanup()
	url := mintDownloadURL(t, ts, tok, "/srv/file")

	var stored model.APIToken
	require.NoError(t, singleton.DB.Where("token_hash = ?", model.HashAPIToken(tok)).
		First(&stored).Error)
	stored.SetServerIDs([]uint64{999})
	require.NoError(t, singleton.DB.Save(&stored).Error)

	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusUnauthorized, resp.StatusCode,
		"download URL must return 401 after PAT server_ids no longer cover the target; body=%s", string(body))
}

func TestTransferConsume_ServerOwnershipChangeIsRejected(t *testing.T) {
	ts, tok, cleanup := setupTransferTest(t, xferAgentDownloadSend([]byte("ok")))
	defer cleanup()
	url := mintDownloadURL(t, ts, tok, "/srv/file")

	srv, _ := singleton.ServerShared.Get(7)
	require.NotNil(t, srv)
	srv.SetUserID(99999)

	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusUnauthorized, resp.StatusCode,
		"download URL must return 401 after server is transferred away from the minting user; body=%s", string(body))
}
