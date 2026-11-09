package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/usersync"
)

// runUserSync places this user's user-scope trees (usersync.Sync): under the
// build's user roots for this OS, with vars, the user's own home (userState),
// expanded, and receipts in stateDir. Failures are only logged.
func runUserSync(ctx context.Context, client usersync.Client, stateDir string, vars map[string]string, logger *slog.Logger) {
	goos := placement.HostOS()
	guard := placement.Guard{
		GOOS:       goos,
		Roots:      placement.Compiled(),
		DataDir:    identity.DataDir(),
		BinaryDirs: placement.BinaryDirs(goos),
		Vars:       vars,
	}
	_, _ = usersync.Sync(ctx, client, guard, filepath.Join(stateDir, "receipts"), logger)
}
