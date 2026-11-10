//go:build darwin

package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"syscall"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The machine-level managed preferences file MDM writes; never the per-user
// ones under it.
var policyPath = "/Library/Managed Preferences/" + identity.Identifier + ".plist"

// readPolicyRoots returns the Machine and User entries of the WriteRoots
// policy. A missing file has none; a file root does not own, or that others
// could write, or that does not parse is ignored with a warning.
func readPolicyRoots() (machine, user []string) {
	info, err := os.Lstat(policyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil {
		err = checkPolicyFile(info)
	}
	var data []byte
	if err == nil {
		data, err = os.ReadFile(policyPath)
	}
	if err == nil {
		machine, user, err = parsePlist(data)
	}
	if err != nil {
		slog.Warn("write roots policy ignored", "path", policyPath, "err", err)
		return nil, nil
	}
	return machine, user
}

// checkPolicyFile trusts only a regular file owned by root that neither its
// group nor others can write: the unprivileged per-user helper reads it too.
func checkPolicyFile(info fs.FileInfo) error {
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
