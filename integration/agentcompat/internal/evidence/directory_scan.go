package evidence

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxEvidenceFiles = 128
	maxEvidenceBytes = 32 << 20
)

func scanDirectory(resultsDir string) (map[string]os.FileInfo, error) {
	if strings.TrimSpace(resultsDir) == "" {
		return nil, errors.New("evidence directory is required")
	}
	info, err := os.Lstat(resultsDir)
	if err != nil {
		return nil, fmt.Errorf("stat evidence directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("evidence path must be a directory")
	}
	if err := validateEvidenceDirectoryMode(info); err != nil {
		return nil, err
	}
	seen := make(map[string]os.FileInfo)
	var totalBytes int64
	err = filepath.WalkDir(resultsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == resultsDir {
			return nil
		}
		if entry.IsDir() {
			relative, err := filepath.Rel(resultsDir, path)
			if err != nil {
				return err
			}
			if relative != "agents" {
				return fmt.Errorf("evidence path is not allowed: %s", relative)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence symlink is not allowed: %s", path)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("evidence file is not regular: %s", path)
		}
		relative, err := filepath.Rel(resultsDir, path)
		if err != nil {
			return err
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !allowedEvidencePath(relative) {
			return fmt.Errorf("evidence path is not allowed: %s", relative)
		}
		if err := validateEvidenceFileMode(fileInfo, relative); err != nil {
			return err
		}
		seen[relative] = fileInfo
		if len(seen) > maxEvidenceFiles {
			return errors.New("too many evidence files")
		}
		totalBytes += fileInfo.Size()
		if totalBytes > maxEvidenceBytes {
			return errors.New("evidence files exceed size limit")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if Redact(string(data)) != string(data) {
			return fmt.Errorf("credential detected in evidence file: %s", path)
		}
		switch filepath.Ext(path) {
		case ".json":
			if !json.Valid(data) {
				return fmt.Errorf("invalid JSON evidence file: %s", path)
			}
		case ".xml":
			var document any
			if err := xml.Unmarshal(data, &document); err != nil {
				return fmt.Errorf("invalid XML evidence file: %s: %w", path, err)
			}
		}
		return nil
	})
	return seen, err
}

func allowedEvidencePath(relative string) bool {
	for _, name := range EvidenceFiles() {
		if name == relative {
			return true
		}
	}
	if filepath.Dir(relative) != "agents" || filepath.Ext(relative) != ".log" {
		return false
	}
	base := strings.TrimSuffix(filepath.Base(relative), ".log")
	if base == "" || base == "." {
		return false
	}
	for _, character := range base {
		if character != '-' && character != '_' && character != '.' && (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func readJSONFile[T any](resultsDir, name string) (T, error) {
	var value T
	data, err := os.ReadFile(filepath.Join(resultsDir, name))
	if err != nil {
		return value, fmt.Errorf("read %s: %w", name, err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("parse %s: %w", name, err)
	}
	return value, nil
}
