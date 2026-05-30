package controller

import (
	"encoding/binary"
	"testing"

	"github.com/nezhahq/nezha/model"
)

// M3 regression: a malicious or corrupt agent can put `> MaxInt64` into the
// size field of an NZTU/NZTD/NZTO frame. Direct uint64→int64 cast wraps to
// a negative value, which bypasses the `hdr.Size > MCPFsTransferMaxSize`
// check (a negative is always less). The guarded reader must reject
// oversize raw u64 BEFORE narrowing.
func TestReadXferFixedHeader_RejectsOversizedUploadSize(t *testing.T) {
	buf := make([]byte, 4+8)
	copy(buf[:4], model.MCPFsXferMagicUploadHdr)
	binary.BigEndian.PutUint64(buf[4:12], uint64(model.MCPFsTransferMaxSize+1))
	_, err := readXferFixedHeaderFromBytes(buf)
	if err == nil {
		t.Fatal("size > MCPFsTransferMaxSize must be rejected; otherwise int64 narrowing lets the upload through with a negative size")
	}
}

func TestReadXferFixedHeader_RejectsOverflowingUploadSize(t *testing.T) {
	buf := make([]byte, 4+8)
	copy(buf[:4], model.MCPFsXferMagicUploadHdr)
	binary.BigEndian.PutUint64(buf[4:12], ^uint64(0))
	_, err := readXferFixedHeaderFromBytes(buf)
	if err == nil {
		t.Fatal("raw u64=MaxUint64 must be rejected before int64 cast wraps it to -1")
	}
}

func TestReadXferFixedHeader_AcceptsLegalUploadSize(t *testing.T) {
	buf := make([]byte, 4+8)
	copy(buf[:4], model.MCPFsXferMagicUploadHdr)
	binary.BigEndian.PutUint64(buf[4:12], 1024)
	hdr, err := readXferFixedHeaderFromBytes(buf)
	if err != nil {
		t.Fatalf("legal size must pass, got %v", err)
	}
	if hdr.Size != 1024 {
		t.Fatalf("want size=1024, got %d", hdr.Size)
	}
}

func TestReadXferFixedHeader_RejectsOversizedDownloadSize(t *testing.T) {
	buf := make([]byte, 4+8+32)
	copy(buf[:4], model.MCPFsXferMagicDownloadHdr)
	binary.BigEndian.PutUint64(buf[4:12], uint64(model.MCPFsTransferMaxSize+1))
	_, err := readXferFixedHeaderFromBytes(buf)
	if err == nil {
		t.Fatal("download size > MCPFsTransferMaxSize must be rejected")
	}
}
