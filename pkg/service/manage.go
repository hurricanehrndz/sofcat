package service

import (
	"path/filepath"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// userSyncRunValue is the HKLM Run value that starts the per-user helper,
// <slug>-useragent.exe, at every logon (site/src/content/docs/guides/zip-items.md).
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
// Windows paths are case-insensitive, so the comparison is too.
func isAgentImage(image string, agents []string) bool {
	image = filepath.Clean(image)
	for _, a := range agents {
		if strings.EqualFold(image, filepath.Clean(a)) {
			return true
		}
	}
	return false
}
