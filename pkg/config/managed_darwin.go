//go:build darwin

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The machine-level managed preferences file MDM writes; never the per-user
// ones under it.
var managedPlistPath = "/Library/Managed Preferences/" + identity.Identifier + ".plist"

// ReadManagedPlist returns the managed preferences file, or nil when there is
// none. It refuses a file root does not own or that its group or others could
// write: the unprivileged per-user helper reads it too.
func ReadManagedPlist() ([]byte, error) {
	info, err := os.Lstat(managedPlistPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil {
		err = checkManagedFile(info)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", managedPlistPath, err)
	}
	return os.ReadFile(managedPlistPath)
}

func checkManagedFile(info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
		return errors.New("not owned by root")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("mode %v is group- or world-writable", info.Mode().Perm())
	}
	return nil
}

// readManaged returns the plist's top-level keys. A missing, untrusted or
// malformed file has none.
func readManaged(warn func(string)) map[string]any {
	data, err := ReadManagedPlist()
	var out map[string]any
	if err == nil && data != nil {
		out, err = parseManagedPlist(data)
	}
	if err != nil {
		warn(fmt.Sprintf("managed settings ignored: %v", err))
		return nil
	}
	return out
}
