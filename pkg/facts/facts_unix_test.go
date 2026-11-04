//go:build !windows

package facts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R41: only a file an administrator could have put there runs as root.
func TestScriptEligible(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode fs.FileMode
		uid  uint32
		ok   bool
	}{
		{"root 0755", 0o755, 0, true},
		{"root 0700", 0o700, 0, true},
		{"root 0500", 0o500, 0, true},
		{"not root", 0o755, 501, false},
		{"group-writable", 0o775, 0, false},
		{"world-writable", 0o757, 0, false},
		{"not executable", 0o644, 0, false},
		{"directory", fs.ModeDir | 0o755, 0, false},
		{"symlink", fs.ModeSymlink | 0o755, 0, false},
	} {
		err := scriptEligible(tc.mode, tc.uid)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
		if err != nil && !errors.Is(err, errSkipped) {
			t.Errorf("%s: %v is not errSkipped, so it would be logged as a failed script", tc.name, err)
		}
	}
}

// The real runner: an eligible script runs directly and a writable one is
// skipped, not run.
func TestRunConditionScriptUnix(t *testing.T) {
	orig := scriptOwnerUID
	scriptOwnerUID = uint32(os.Getuid())
	t.Cleanup(func() { scriptOwnerUID = orig })

	dir := t.TempDir()
	good := filepath.Join(dir, "10-good")
	writable := filepath.Join(dir, "20-writable")
	for _, p := range []string{good, writable} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho '{\"site\": \"east\"}'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(writable, 0o777); err != nil {
		t.Fatal(err)
	}
	out, err := runConditionScript(good)
	if err != nil || strings.TrimSpace(string(out)) != `{"site": "east"}` {
		t.Fatalf("good script: %q, %v", out, err)
	}
	if _, err := runConditionScript(writable); !errors.Is(err, errSkipped) {
		t.Fatalf("world-writable script: err = %v, want errSkipped", err)
	}
}
