//go:build !windows

package main

import (
	"context"
	"log/slog"

	"github.com/hurricanehrndz/sofcat/pkg/usersync"
)

// runUserSync is a no-op off Windows: user-scope placements exist only on
// Windows in v1, so macOS and Linux builds keep building and the agent
// exits 0 at once.
func runUserSync(_ context.Context, _ usersync.Client, logger *slog.Logger) {
	logger.Info("user sync: user-scope placements are Windows-only; nothing to do")
}
