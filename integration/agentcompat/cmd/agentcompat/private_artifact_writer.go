//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	scenarioArtifactPublished = func(string) error { return nil }
	privateArtifactClose      = (*os.File).Close
	privateArtifactRemove     = os.Remove
)

func writePrivateArtifact(path string, data []byte) (err error) {
	return writePrivateArtifactWithSeam(path, data, func() error {
		return scenarioArtifactPublished(filepath.Base(path))
	})
}

func writePrivateArtifactWithSeam(path string, data []byte, beforeRename func() error) (err error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".artifact-*")
	if err != nil {
		return fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	closed := false
	defer func() {
		if !committed {
			if !closed {
				err = errors.Join(err, privateArtifactClose(temporary))
			}
			err = errors.Join(err, privateArtifactRemove(temporaryPath))
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary artifact: %w", err)
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write temporary artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary artifact: %w", err)
	}
	closeErr := privateArtifactClose(temporary)
	closed = true
	if closeErr != nil {
		return fmt.Errorf("close temporary artifact: %w", closeErr)
	}
	if err := beforeRename(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("rename temporary artifact: %w", err)
	}
	committed = true
	return nil
}
