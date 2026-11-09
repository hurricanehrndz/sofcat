//go:build windows

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/usersync"
)

// runUserSync places this user's user-scope trees (usersync.Sync): under the
// build's windows_user roots, expanded with the user's own %USERPROFILE%,
// with receipts in %LOCALAPPDATA%\<Name>\receipts. Failures are only logged.
func runUserSync(ctx context.Context, client usersync.Client, logger *slog.Logger) {
	profile, localAppData := os.Getenv("USERPROFILE"), os.Getenv("LOCALAPPDATA")
	if profile == "" || localAppData == "" {
		logger.Warn("user sync: USERPROFILE or LOCALAPPDATA is not set; nothing placed")
		return
	}
	guard := placement.Guard{
		GOOS:       placement.Windows,
		Roots:      placement.Compiled(),
		DataDir:    identity.DataDir(),
		BinaryDirs: placement.BinaryDirs(placement.Windows),
		Vars:       map[string]string{"USERPROFILE": profile},
	}
	receiptDir := filepath.Join(localAppData, identity.Name, "receipts")
	_, _ = usersync.Sync(ctx, client, guard, receiptDir, logger)
}
