//go:build darwin

package service

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// StartUserSync starts the per-user helper, <slug>-useragent, through its
// LaunchAgent in the GUI session of each user signed in to the Mac, fast
// user-switched ones included, so a new user-placement plan reaches their
// homes without waiting for a logon (startUserSyncLaunchd). launchd runs it
// as the user, with the user's environment, and the service does not wait
// for it. launchctl needs root to reach other users' domains.
func StartUserSync(appDataPath string) {
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("user sync: unable to find the helper", "err", err)
		return
	}
	// Always next to this executable, never from config.
	helper := filepath.Join(filepath.Dir(exe), identity.Slug+"-useragent")
	startUserSyncLaunchd(appDataPath, helper, identity.UserAgentLabel(), identity.UserAgentPlistPath())
}
