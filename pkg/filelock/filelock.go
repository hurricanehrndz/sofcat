// Package filelock holds an exclusive, advisory lock on a file across
// processes. It is the kernel's file lock (flock on unix, LockFileEx on
// Windows), so the lock goes with the handle: a crashed holder never
// strands it. Callers never delete the lock file, since deleting one races.
package filelock

import (
	"errors"
	"os"
)

// ErrLocked is TryLock's error when another handle holds the lock.
var ErrLocked = errors.New("file is locked")

// Lock blocks until it holds the lock on path, creating the file if needed,
// and returns the function that releases it.
func Lock(path string) (func(), error) {
	return lock(path, true)
}

// TryLock takes the lock on path as Lock does, or fails at once with
// ErrLocked when another handle holds it.
func TryLock(path string) (func(), error) {
	return lock(path, false)
}

func lock(path string, wait bool) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f, wait); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
