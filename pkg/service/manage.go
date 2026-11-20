package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// startProgress is where a service that was just started stands, as
// waitRunning reads it from the service manager.
type startProgress int

const (
	startPending startProgress = iota
	startRunning
	startFailed
)

// waitRunning polls query every poll until the service named name runs, and
// fails when it stops instead or is still starting after timeout. query also
// returns a description of the state for the error. A service only reports
// running once it is ready to serve, so the outcome of its runs never
// counts here.
func waitRunning(name string, query func() (startProgress, string, error), timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		progress, state, err := query()
		if err != nil {
			return fmt.Errorf("failed querying service %q: %w", name, err)
		}
		switch progress {
		case startRunning:
			return nil
		case startFailed:
			return fmt.Errorf("service %q did not stay running (%s)", name, state)
		case startPending:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for service %q to start (%s)", name, state)
		}
		time.Sleep(poll)
	}
}

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
