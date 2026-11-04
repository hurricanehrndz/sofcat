//go:build !windows

package facts

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// scriptOwnerUID is who must own an admin script: root. Tests, which cannot
// create root-owned files, set it to their own uid.
var scriptOwnerUID uint32

// scriptCommand runs an admin script directly when it is one an
// administrator put there (see scriptEligible); other files are skipped.
func scriptCommand(path string) (string, []string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", nil, fmt.Errorf("%w: cannot read the owner of %s", errSkipped, path)
	}
	if err := scriptEligible(info.Mode(), st.Uid); err != nil {
		return "", nil, err
	}
	return path, nil, nil
}

// scriptEligible is the macOS admin-script rule: a regular file owned by
// scriptOwnerUID, not group- or world-writable, and executable. The data
// directory is root-only already; the rule also keeps a file a user could
// have changed from running as root if that ever stops holding.
func scriptEligible(mode fs.FileMode, uid uint32) error {
	switch {
	case !mode.IsRegular():
		return fmt.Errorf("%w: not a regular file", errSkipped)
	case uid != scriptOwnerUID:
		return fmt.Errorf("%w: owned by uid %d, not %d", errSkipped, uid, scriptOwnerUID)
	case mode.Perm()&0o022 != 0:
		return fmt.Errorf("%w: group- or world-writable (%04o)", errSkipped, mode.Perm())
	case mode.Perm()&0o111 == 0:
		return fmt.Errorf("%w: not executable (%04o)", errSkipped, mode.Perm())
	}
	return nil
}
