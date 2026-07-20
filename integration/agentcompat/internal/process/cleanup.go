//go:build linux

package process

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type CleanupRecord struct {
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	Forced bool   `json:"forced"`
	Error  string `json:"error,omitempty"`
}

type CleanupReceipt struct {
	Passed    bool            `json:"passed"`
	Forced    bool            `json:"forced"`
	Processes []CleanupRecord `json:"processes"`
}

func NewCleanupReceipt(records []CleanupRecord) CleanupReceipt {
	receipt := CleanupReceipt{Passed: true, Processes: append([]CleanupRecord(nil), records...)}
	for _, record := range records {
		receipt.Forced = receipt.Forced || record.Forced
		receipt.Passed = receipt.Passed && record.Error == ""
	}
	return receipt
}

func WriteCleanupReceipt(path string, receipt CleanupReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cleanup receipt: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create cleanup receipt directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write cleanup receipt: %w", err)
	}
	return nil
}
