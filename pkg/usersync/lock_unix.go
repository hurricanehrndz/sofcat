//go:build unix

package usersync

import (
	"os"
	"syscall"
)

// lockFile blocks until it holds an exclusive lock on f. The kernel drops
// the lock when f closes, or the process exits.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}
