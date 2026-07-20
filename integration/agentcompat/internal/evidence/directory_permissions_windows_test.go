//go:build windows

package evidence

import (
	"os"
	"testing"
	"time"
)

func TestEvidence_WindowsPermissionValidationIgnoresSyntheticModes(t *testing.T) {
	info := permissionTestFileInfo{mode: 0o666}

	if err := validateEvidenceDirectoryMode(info); err != nil {
		t.Fatalf("validate evidence directory: %v", err)
	}
	if err := validateEvidenceFileMode(info, "metadata.json"); err != nil {
		t.Fatalf("validate evidence file: %v", err)
	}
}

type permissionTestFileInfo struct {
	mode os.FileMode
}

func (info permissionTestFileInfo) Name() string       { return "evidence" }
func (info permissionTestFileInfo) Size() int64        { return 0 }
func (info permissionTestFileInfo) Mode() os.FileMode  { return info.mode }
func (info permissionTestFileInfo) ModTime() time.Time { return time.Time{} }
func (info permissionTestFileInfo) IsDir() bool        { return false }
func (info permissionTestFileInfo) Sys() any           { return nil }
