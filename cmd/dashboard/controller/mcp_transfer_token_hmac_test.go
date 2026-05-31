package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A transfer token whose HMAC signature does not match the stored entry
// must be rejected at consume time. The signature is documented as
// "HMAC-SHA256 防篡改" (tamper-proof); if consume never verifies it, the
// guarantee is hollow. This test pins that the signature is actually
// checked: a stored entry under a token id with a forged/altered sig must
// not be consumable.
func TestConsumeTransferToken_RejectsTamperedSignature(t *testing.T) {
	e := transferEntry{
		UserID:    1,
		TokenID:   2,
		ServerID:  3,
		Path:      "/srv/file",
		Direction: transferDirDownload,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	tok, err := mintTransferToken(e)
	require.NoError(t, err)
	require.Contains(t, tok, ".", "token must carry an id.sig shape")

	// Flip the last hex nibble of the signature to forge a mismatching MAC
	// while keeping the same random id portion.
	idx := strings.LastIndex(tok, ".")
	require.Greater(t, idx, 0)
	id, sig := tok[:idx], tok[idx+1:]
	last := sig[len(sig)-1]
	var flipped byte
	if last == '0' {
		flipped = '1'
	} else {
		flipped = '0'
	}
	forged := id + "." + sig[:len(sig)-1] + string(flipped)

	// Re-store the entry under the forged token so the map lookup itself
	// would succeed — only the HMAC check should reject it.
	transferEntries.Store(forged, e)
	t.Cleanup(func() { transferEntries.Delete(forged) })

	got, err := consumeTransferToken(forged, transferDirDownload)
	require.Error(t, err, "tampered-signature token must be rejected")
	require.Nil(t, got)
}

// A correctly minted token must still consume successfully and be single-use.
func TestConsumeTransferToken_ValidSignatureRoundTrips(t *testing.T) {
	e := transferEntry{
		UserID:    10,
		TokenID:   20,
		ServerID:  30,
		Path:      "/srv/other",
		Direction: transferDirUpload,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	tok, err := mintTransferToken(e)
	require.NoError(t, err)

	got, err := consumeTransferToken(tok, transferDirUpload)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, e.Path, got.Path)

	// single-use: second consume must fail.
	_, err = consumeTransferToken(tok, transferDirUpload)
	require.Error(t, err)
}
