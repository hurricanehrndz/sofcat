//go:build windows

package usersync

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile blocks until it holds an exclusive lock on f. Windows drops the
// lock when the handle closes, or the process exits.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}
