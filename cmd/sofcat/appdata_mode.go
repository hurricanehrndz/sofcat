//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The owner-and-mode protection macOS uses (appdata_acl_darwin.go). It is
// portable to every Unix so its tests run on Linux too.

// chownFunc changes an owner without following a symlink; tests replace it,
// since only root may hand a file to root.
var chownFunc = os.Lchown

// protectRootOnlyDir is the macOS twin of the Windows data directory ACL: the
// Data directory (the self-serve manifest, cache, receipts and admin fact
// scripts) becomes root-owned with mode 0700, so no other user can read it or
// add a script that runs as root. Its parent stays 0755, because placements
// and the CLI live there too.
func protectRootOnlyDir(root string) error {
	// CEILING: only <anything>/<Name>/Data is protected, so a mistyped
	// app_data_path such as /Library cannot have its owner and mode replaced.
	// Upgrade: an explicit config opt-in for other names if anyone needs one.
	if filepath.Base(filepath.Dir(root)) != identity.Name || filepath.Base(root) != "Data" {
		return fmt.Errorf("not protecting %s: the directory is not %s/Data", root, identity.Name)
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	// A symlink here would point the owner and mode at somebody else's
	// directory.
	if fi, err := os.Lstat(root); err != nil {
		return err
	} else if !fi.IsDir() {
		return fmt.Errorf("not protecting %s: not a plain directory", root)
	}
	if err := chownFunc(root, 0, 0); err != nil {
		return err
	}
	return os.Chmod(root, 0o700)
}

// protectConfigFile makes config.yaml, which sits beside Data rather than in
// it and can hold auth_pass and TLS key paths, root-owned with mode 0600. A
// missing config is left alone (the pkg installs none), and anything but a
// regular file is refused, so a symlink cannot redirect the chmod.
func protectConfigFile(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("not protecting %s: not a regular file", path)
	}
	if err := chownFunc(path, 0, 0); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
