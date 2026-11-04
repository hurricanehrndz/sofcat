//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// fakeChown records chownFunc calls, so the owner logic runs unprivileged.
func fakeChown(t *testing.T) *[]string {
	t.Helper()
	orig := chownFunc
	t.Cleanup(func() { chownFunc = orig })
	var chowned []string
	chownFunc = func(path string, uid, gid int) error {
		if uid != 0 || gid != 0 {
			t.Errorf("chown %s to %d:%d, want root", path, uid, gid)
		}
		chowned = append(chowned, path)
		return nil
	}
	return &chowned
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestProtectRootOnlyDir(t *testing.T) {
	chowned := fakeChown(t)
	root := filepath.Join(t.TempDir(), identity.Name, "Data")
	if err := os.MkdirAll(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := protectRootOnlyDir(root); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, root); got != 0o700 {
		t.Errorf("mode = %v, want 0700", got)
	}
	if len(*chowned) != 1 || (*chowned)[0] != root {
		t.Errorf("chowned %q, want [%s]", *chowned, root)
	}
}

// A mistyped app_data_path such as /Library must keep its owner and mode.
func TestProtectRootOnlyDirRefusesOtherNames(t *testing.T) {
	chowned := fakeChown(t)
	base := t.TempDir()
	for _, root := range []string{
		filepath.Join(base, "Library"),
		filepath.Join(base, identity.Name),
		filepath.Join(base, "Other", "Data"),
	} {
		if err := protectRootOnlyDir(root); err == nil || !strings.Contains(err.Error(), "not protecting") {
			t.Errorf("%s: err = %v, want a refusal", root, err)
		}
	}
	if len(*chowned) != 0 {
		t.Errorf("chowned %q", *chowned)
	}
}

// A Data symlink would hand somebody else's directory to root.
func TestProtectRootOnlyDirRefusesSymlink(t *testing.T) {
	chowned := fakeChown(t)
	base := t.TempDir()
	target := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, identity.Name, "Data")
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, root); err != nil {
		t.Fatal(err)
	}
	if err := protectRootOnlyDir(root); err == nil || !strings.Contains(err.Error(), "not a plain directory") {
		t.Fatalf("err = %v, want a symlink refusal", err)
	}
	if got := mode(t, target); got != 0o755 {
		t.Errorf("target mode = %v, want 0755", got)
	}
	if len(*chowned) != 0 {
		t.Errorf("chowned %q", *chowned)
	}
}

// config.yaml can hold auth_pass, and the pkg's umask or an admin's cp can
// leave it world-readable beside the 0700 Data directory.
func TestProtectConfigFile(t *testing.T) {
	chowned := fakeChown(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("auth_pass: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := protectConfigFile(cfg); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, cfg); got != 0o600 {
		t.Errorf("mode = %v, want 0600", got)
	}
	if len(*chowned) != 1 || (*chowned)[0] != cfg {
		t.Errorf("chowned %q, want [%s]", *chowned, cfg)
	}

	// The pkg installs no config: nothing to do.
	if err := protectConfigFile(filepath.Join(dir, "missing.yaml")); err != nil {
		t.Errorf("missing config: %v", err)
	}

	// A symlink must not redirect the chown and chmod.
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := protectConfigFile(link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("symlink: err = %v, want a refusal", err)
	}
	if got := mode(t, target); got != 0o644 {
		t.Errorf("target mode = %v, want 0644", got)
	}
	if len(*chowned) != 1 {
		t.Errorf("chowned %q, want only the config", *chowned)
	}
}
