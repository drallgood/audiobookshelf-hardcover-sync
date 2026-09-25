package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrStateFileLocked indicates another process currently owns the state-file
// lock. Lock acquisition is nonblocking so callers can report a busy state.
var ErrStateFileLocked = errors.New("state file is locked")

// FileLock is an exclusive lock on a state file's sidecar lock file. Closing
// it releases the operating-system lock; the sidecar itself is intentionally
// retained because deleting a locked file can let another process lock a new
// inode at the same path.
type FileLock struct {
	file     *os.File
	unlock   func() error
	once     sync.Once
	closeErr error
}

// AcquireFileLock acquires an exclusive, nonblocking lock for path. It must
// cover the complete load/change/save transaction. The OS releases the lock if
// the owning process exits unexpectedly, so a crashed process cannot leave a
// stale lock behind.
func AcquireFileLock(path string) (*FileLock, error) {
	resolvedPath, err := resolveStatePath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve state file for locking: %w", err)
	}
	lockPath := resolvedPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create state lock directory: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open state lock file: %w", err)
	}
	unlock, err := lockStateFile(file)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, ErrStateFileLocked) {
			return nil, fmt.Errorf("%w: %s", ErrStateFileLocked, resolvedPath)
		}
		return nil, fmt.Errorf("failed to acquire state file lock: %w", err)
	}
	return &FileLock{file: file, unlock: unlock}, nil
}

// Close releases the OS lock and closes the sidecar file. It is safe to call
// Close more than once.
func (l *FileLock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.unlock != nil {
			if err := l.unlock(); err != nil {
				l.closeErr = fmt.Errorf("failed to release state file lock: %w", err)
			}
		}
		if l.file != nil {
			if err := l.file.Close(); err != nil {
				if l.closeErr != nil {
					l.closeErr = errors.Join(l.closeErr, fmt.Errorf("failed to close state lock file: %w", err))
				} else {
					l.closeErr = fmt.Errorf("failed to close state lock file: %w", err)
				}
			}
		}
	})
	return l.closeErr
}
