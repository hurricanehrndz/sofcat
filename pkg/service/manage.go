package service

import "github.com/hurricanehrndz/sofcat/pkg/identity"

// userSyncRunValue is the HKLM Run value that starts the per-user helper,
// <slug>-ui.exe --user-sync, at every logon (site/src/content/docs/guides/zip-items.md).
// -serviceinstall writes it and -serviceremove removes it, so the installer
// never touches it. uiExe is the helper's full path.
func userSyncRunValue(uiExe string) (name, data string) {
	return identity.Slug + "-user-sync", `"` + uiExe + `" --user-sync`
}
