//go:build !windows

package state

import (
	"fmt"
	"os"
)

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open directory: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("failed to close directory: %w", err)
	}

	return nil
}
