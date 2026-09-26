//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package state

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openStateLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("state lock file must not be a symlink: %w", err)
		}
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func lockStateFile(file *os.File) (func() error, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrStateFileLocked
		}
		return nil, err
	}
	return func() error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }, nil
}
