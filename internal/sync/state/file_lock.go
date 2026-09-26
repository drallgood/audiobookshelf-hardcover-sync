package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	file      *os.File
	statePath string
	unlock    func() error
	once      sync.Once
	closeErr  error
}

// AcquireFileLock acquires an exclusive, nonblocking lock for path. It must
// cover the complete load/change/save transaction. The OS releases the lock if
// the owning process exits unexpectedly, so a crashed process cannot leave a
// stale lock behind.
func AcquireFileLock(path string) (*FileLock, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("state file path is required for locking")
	}
	resolvedPath, err := resolveStatePath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve state file for locking: %w", err)
	}
	lockPath := resolvedPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create state lock directory: %w", err)
	}
	file, err := openStateLockFile(lockPath)
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
	return &FileLock{file: file, statePath: resolvedPath, unlock: unlock}, nil
}

// StatePath is the resolved state-file path protected by this lock. Callers
// must use it for reads and writes until Close, even if the configured path is
// a symlink that changes target during the transaction.
func (l *FileLock) StatePath() string {
	if l == nil {
		return ""
	}
	return l.statePath
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
