// Command sofcat-useragent, built as <slug>-useragent, is the per-user helper:
// it places the signed-in user's user-scope zip items into their own profile
// (pkg/usersync) and exits, with no window. What -serviceinstall writes
// starts it at every logon: the HKLM Run value on Windows, the LaunchAgent
// /Library/LaunchAgents/<identifier>.useragent.plist on macOS. The service
// starts it in each signed-in session whenever it saves a new user-placement
// plan: directly, with -no-retry, on Windows; through the LaunchAgent, whose
// arguments are fixed, on macOS. It runs as the user, never as SYSTEM or root.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/service"
	"github.com/hurricanehrndz/sofcat/pkg/usersync"
)

func main() {
	pipeName := flag.String("pipe-name", identity.PipeName(), "service pipe name")
	noRetry := flag.Bool("no-retry", false, "do not wait for an unreachable service (set when the service starts the helper)")
	flag.Parse()

	// Stderr is invisible in the -H windowsgui build; this is for a console run.
	stateDir, vars, stateErr := userState()
	logger, logWriter, logErr := setupLogger(stateDir)
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "%s user agent diagnostics unavailable: %v\n", identity.Name, logErr)
	}
	if logWriter != nil {
		defer func() { _ = logWriter.Close() }()
	}
	// The zip installer the sync runs logs through slog's default.
	slog.SetDefault(logger)

	if *noRetry {
		usersync.PlanRetryFor = 0
	}
	if stateErr != nil {
		logger.Warn("user sync: nothing placed", "err", stateErr)
		return
	}
	// Exit 0 even when an item fails: every result is logged and reported.
	runUserSync(context.Background(), service.NewClient(*pipeName), stateDir, vars, logger)
}
