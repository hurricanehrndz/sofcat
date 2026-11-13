//go:build unix

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on f, waiting for it when wait is set.
// The kernel drops the lock when f closes, or the process exits.
func lockFile(f *os.File, wait bool) error {
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	err := syscall.Flock(int(f.Fd()), how)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}
