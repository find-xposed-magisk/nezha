//go:build windows

package evidence

import "os"

func validateEvidenceDirectoryMode(os.FileInfo) error {
	return nil
}

func validateEvidenceFileMode(os.FileInfo, string) error {
	return nil
}
