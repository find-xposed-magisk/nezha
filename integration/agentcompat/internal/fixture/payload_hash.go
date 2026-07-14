package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

const verifierBufferBytes = 1024 * 1024

type PayloadDigest struct {
	Bytes  uint64
	SHA256 [sha256.Size]byte
}

func (d PayloadDigest) Hex() string {
	return hex.EncodeToString(d.SHA256[:])
}

func VerifyPayload(reader io.Reader, expectedBytes uint64) (PayloadDigest, error) {
	if expectedBytes > contract.TransferBytes {
		return PayloadDigest{}, ErrPayloadOverrun
	}
	hash := sha256.New()
	buffer := make([]byte, verifierBufferBytes)
	limited := io.LimitReader(reader, int64(expectedBytes)+1)
	written, err := io.CopyBuffer(hash, limited, buffer)
	if err != nil {
		return PayloadDigest{}, fmt.Errorf("verify payload: %w", err)
	}
	if uint64(written) > expectedBytes {
		return PayloadDigest{}, ErrPayloadOverrun
	}
	if uint64(written) != expectedBytes {
		return PayloadDigest{}, ErrPayloadSizeMismatch
	}
	digest := PayloadDigest{Bytes: uint64(written)}
	copy(digest.SHA256[:], hash.Sum(nil))
	return digest, nil
}
