package evidence

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxEvidenceFiles = 128
	maxEvidenceBytes = 32 << 20
)

type evidenceFile struct {
	info os.FileInfo
	data []byte
}

type evidenceSnapshot map[string]evidenceFile

func scanDirectory(root *os.Root) (evidenceSnapshot, error) {
	info, err := root.Lstat(".")
	if err != nil {
		return nil, fmt.Errorf("stat evidence directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("evidence path must be a directory")
	}
	if err := validateEvidenceDirectoryMode(info); err != nil {
		return nil, err
	}
	seen := make(evidenceSnapshot)
	var totalBytes int64
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		if entry.IsDir() {
			if path != "agents" {
				return fmt.Errorf("evidence path is not allowed: %s", path)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence symlink is not allowed: %s", path)
		}
		if !allowedEvidencePath(path) {
			return fmt.Errorf("evidence path is not allowed: %s", path)
		}
		file, err := root.Open(path)
		if err != nil {
			return fmt.Errorf("open evidence file %s: %w", path, err)
		}
		fileInfo, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("stat evidence file %s: %w", path, err)
		}
		if !fileInfo.Mode().IsRegular() {
			_ = file.Close()
			return fmt.Errorf("evidence file is not regular: %s", path)
		}
		if err := validateEvidenceFileMode(fileInfo, path); err != nil {
			_ = file.Close()
			return err
		}
		if len(seen)+1 > maxEvidenceFiles {
			_ = file.Close()
			return errors.New("too many evidence files")
		}
		remaining := int64(maxEvidenceBytes) - totalBytes
		data, readErr := io.ReadAll(io.LimitReader(file, remaining+1))
		closeErr := file.Close()
		if readErr != nil {
			return fmt.Errorf("read evidence file %s: %w", path, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close evidence file %s: %w", path, closeErr)
		}
		if int64(len(data)) > remaining {
			return errors.New("evidence files exceed size limit")
		}
		totalBytes += int64(len(data))
		if Redact(string(data)) != string(data) {
			return fmt.Errorf("credential detected in evidence file: %s", path)
		}
		switch extension(path) {
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
		seen[path] = evidenceFile{info: fileInfo, data: data}
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

func readJSONFile[T any](files evidenceSnapshot, name string) (T, error) {
	var value T
	file, exists := files[name]
	if !exists {
		return value, fmt.Errorf("read %s: evidence snapshot is missing", name)
	}
	if err := json.Unmarshal(file.data, &value); err != nil {
		return value, fmt.Errorf("parse %s: %w", name, err)
	}
	return value, nil
}

func extension(path string) string {
	index := strings.LastIndexByte(path, '.')
	if index < 0 {
		return ""
	}
	return path[index:]
}
