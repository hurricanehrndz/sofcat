package filelock

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestLock: while one handle holds the lock, TryLock refuses and Lock waits;
// releasing it lets the waiter in. Two handles in one process conflict as two
// processes do (flock is per open file, LockFileEx per handle), so this covers
// the cross-process case on both platforms.
func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock while held: err = %v, want ErrLocked", err)
	}

	type result struct {
		unlock func()
		err    error
	}
	acquired := make(chan result, 1)
	go func() {
		u, lockErr := Lock(path)
		acquired <- result{u, lockErr}
	}()
	select {
	case <-acquired:
		t.Fatal("Lock returned while another handle held the lock")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case r := <-acquired:
		if r.err != nil {
			t.Fatal(r.err)
		}
		r.unlock()
	case <-time.After(5 * time.Second):
		t.Fatal("Lock did not return after the lock was released")
	}

	unlock, err = TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	unlock()
}
