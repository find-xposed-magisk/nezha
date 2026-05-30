package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/nezhahq/nezha/model"
)

// M2 regression: download finalisation must validate the trailing NZTO
// frame's declared size AND sha256 against what was actually streamed.
// The old relay only checked the 4-byte magic, so a truncated NZTO (no
// hash) or a wrong-hash payload was silently accepted.
func TestValidateDownloadFinal_RejectsTruncatedNZTO(t *testing.T) {
	buf := make([]byte, 4)
	copy(buf, model.MCPFsXferMagicOK)
	if err := validateDownloadFinal(buf, 0, sha256.New().Sum(nil)); err == nil {
		t.Fatal("a 4-byte NZTO (magic only, no size+sha) must be rejected")
	}
}

func TestValidateDownloadFinal_RejectsSizeMismatch(t *testing.T) {
	h := sha256.New()
	h.Write([]byte("payload"))
	sum := h.Sum(nil)

	buf := make([]byte, 4+8+32)
	copy(buf[:4], model.MCPFsXferMagicOK)
	binary.BigEndian.PutUint64(buf[4:12], 999) // declared size 999
	copy(buf[12:44], sum)

	if err := validateDownloadFinal(buf, int64(len("payload")), sum); err == nil {
		t.Fatal("declared size mismatch with actual streamed bytes must be rejected")
	}
}

func TestValidateDownloadFinal_RejectsHashMismatch(t *testing.T) {
	declared := []byte("declared")
	streamed := []byte("streamed-something-else")
	h := sha256.New()
	h.Write(declared)
	declaredHash := h.Sum(nil)

	streamedH := sha256.New()
	streamedH.Write(streamed)
	streamedHash := streamedH.Sum(nil)

	buf := make([]byte, 4+8+32)
	copy(buf[:4], model.MCPFsXferMagicOK)
	binary.BigEndian.PutUint64(buf[4:12], uint64(len(streamed)))
	copy(buf[12:44], declaredHash)

	if err := validateDownloadFinal(buf, int64(len(streamed)), streamedHash); err == nil {
		t.Fatal("declared sha256 != streamed sha256 must be rejected")
	}
}

func TestValidateDownloadFinal_AcceptsMatchingSizeAndHash(t *testing.T) {
	payload := []byte("hello world")
	h := sha256.New()
	h.Write(payload)
	sum := h.Sum(nil)

	buf := make([]byte, 4+8+32)
	copy(buf[:4], model.MCPFsXferMagicOK)
	binary.BigEndian.PutUint64(buf[4:12], uint64(len(payload)))
	copy(buf[12:44], sum)

	if err := validateDownloadFinal(buf, int64(len(payload)), sum); err != nil {
		t.Fatalf("matching final header must pass, got %v", err)
	}
}

// Hash skip: agent may omit the sha when the source filesystem can't
// produce one (e.g. live device). Encode as all-zero sha256; that's a
// legal but explicit "no hash" signal. Size must still match.
func TestValidateDownloadFinal_AllowsAllZeroHashAsExplicitSkip(t *testing.T) {
	payload := []byte("nothash")
	streamedHash, _ := hex.DecodeString("0000000000000000000000000000000000000000000000000000000000000000")

	buf := make([]byte, 4+8+32)
	copy(buf[:4], model.MCPFsXferMagicOK)
	binary.BigEndian.PutUint64(buf[4:12], uint64(len(payload)))
	// declared bytes 12-44 already zero by make()

	if err := validateDownloadFinal(buf, int64(len(payload)), streamedHash); err != nil {
		t.Fatalf("all-zero declared hash with matching size must pass (explicit skip), got %v", err)
	}
}

// Defence-in-depth: the magic must still match. validateDownloadFinal is
// reached after the relay already checked it, but a second check costs
// nothing and survives future refactors that split the parsing.
func TestValidateDownloadFinal_RejectsWrongMagic(t *testing.T) {
	buf := make([]byte, 4+8+32)
	copy(buf[:4], []byte("XXXX"))
	if err := validateDownloadFinal(buf, 0, sha256.New().Sum(nil)); err == nil {
		t.Fatal("non-NZTO magic must be rejected")
	}
}

// Defence-in-depth: bytes.Compare of slices of different length still
// returns non-zero, but Go semantics for hex.EncodeToString are wider
// than 32 bytes. Pin that the validator only inspects the first 32 hash
// bytes.
func TestValidateDownloadFinal_OnlyConsiders32HashBytes(t *testing.T) {
	payload := []byte("X")
	h := sha256.New()
	h.Write(payload)
	sum := h.Sum(nil)

	buf := make([]byte, 4+8+32)
	copy(buf[:4], model.MCPFsXferMagicOK)
	binary.BigEndian.PutUint64(buf[4:12], uint64(len(payload)))
	copy(buf[12:44], sum)
	streamedHashExtra := append(bytes.Clone(sum), 0xAA, 0xBB)

	if err := validateDownloadFinal(buf, int64(len(payload)), streamedHashExtra); err != nil {
		t.Fatalf("validator must compare exactly the first 32 streamed hash bytes, got %v", err)
	}
}
