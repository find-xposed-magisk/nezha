//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package evidence

import (
	"errors"
	"fmt"
	"os"
)

func validateEvidenceDirectoryMode(info os.FileInfo) error {
	if info.Mode().Perm() != 0o700 {
		return errors.New("evidence directory must use mode 0700")
	}
	return nil
}

func validateEvidenceFileMode(info os.FileInfo, relative string) error {
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("evidence file must use mode 0600: %s", relative)
	}
	return nil
}
