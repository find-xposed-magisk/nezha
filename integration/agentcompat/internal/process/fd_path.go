//go:build linux

package process

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ProcessHasOpenPath(pid int, path string) (bool, error) {
	if pid < 1 || !filepath.IsAbs(path) {
		return false, errors.New("invalid process path query")
	}
	directory := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, err
	}
	wanted := filepath.Clean(path)
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(directory, entry.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		target = strings.TrimSuffix(target, " (deleted)")
		if filepath.IsAbs(target) && filepath.Clean(target) == wanted {
			return true, nil
		}
	}
	return false, nil
}
