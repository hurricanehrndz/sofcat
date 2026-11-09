package service

import "github.com/hurricanehrndz/sofcat/pkg/identity"

// userSyncRunValue is the HKLM Run value that starts the per-user helper,
// <slug>-useragent.exe, at every logon (site/src/content/docs/guides/zip-items.md).
// -serviceinstall writes it and -serviceremove removes it, so the installer
// never touches it. agentExe is the helper's full path. The value keeps the
// name it had when the storefront was the helper (<slug>-ui.exe --user-sync),
// so an upgrade overwrites it rather than leaving a stale one behind.
func userSyncRunValue(agentExe string) (name, data string) {
	return identity.Slug + "-user-sync", `"` + agentExe + `"`
}
