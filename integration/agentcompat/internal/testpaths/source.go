package testpaths

import (
	"errors"
	"os"
	"path/filepath"
)

func NezhaSource(start string) (string, error) {
	if configured := os.Getenv("NEZHA_SOURCE"); configured != "" {
		return absoluteDirectory(configured)
	}
	return findModuleRoot(start)
}

func AgentSource(nezhaSource string) (string, error) {
	if configured := os.Getenv("AGENT_SOURCE"); configured != "" {
		return absoluteDirectory(configured)
	}
	root, err := absoluteDirectory(nezhaSource)
	if err != nil {
		return "", err
	}
	return absoluteDirectory(filepath.Join(filepath.Dir(root), "agent"))
}

func absoluteDirectory(raw string) (string, error) {
	if raw == "" || !filepath.IsAbs(raw) {
		return "", errors.New("source path must be absolute")
	}
	clean := filepath.Clean(raw)
	info, err := os.Stat(clean)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("source path must be a directory")
	}
	return clean, nil
}

func findModuleRoot(start string) (string, error) {
	if start == "" {
		start, _ = os.Getwd()
	}
	absolute, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(absolute); statErr == nil && !info.IsDir() {
		absolute = filepath.Dir(absolute)
	}
	for directory := absolute; ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("module root not found")
		}
	}
}
