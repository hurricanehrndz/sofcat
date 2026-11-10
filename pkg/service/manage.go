package service

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// userSyncRunValue is the HKLM Run value that starts the per-user helper,
// <slug>-useragent.exe, at every logon (site/src/content/docs/zip-items/overview.md).
// -serviceinstall writes it and -serviceremove removes it, so the installer
// never touches it. agentExe is the helper's full path. The value keeps the
// name it had when the storefront was the helper (<slug>-ui.exe --user-sync),
// so an upgrade overwrites it rather than leaving a stale one behind.
func userSyncRunValue(agentExe string) (name, data string) {
	return identity.Slug + "-user-sync", `"` + agentExe + `"`
}

// isAgentImage reports whether image, a running process's full image path,
// is one of agents, the full paths of the executables -serviceremove ends
// (the helper and the storefront).
// Windows paths are case-insensitive, so the comparison is too. A path that
// differs as a string can still name the same file: an 8.3 short name, a
// \\?\ prefix or a junction, when -serviceremove was started through one.
// The string comparison is the fast path; file identity decides the rest,
// and a path that cannot be read matches nothing.
func isAgentImage(image string, agents []string) bool {
	image = filepath.Clean(image)
	for _, a := range agents {
		if strings.EqualFold(image, filepath.Clean(a)) {
			return true
		}
	}
	fi, err := os.Stat(image)
	if err != nil {
		return false
	}
	for _, a := range agents {
		if ai, err := os.Stat(a); err == nil && os.SameFile(fi, ai) {
			return true
		}
	}
	return false
}
