package placement

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// WithPolicy returns r plus the write roots managed policy adds on goos (see
// pkg/placement/policy), dropping each entry that is not a valid root for its
// scope with a warning, and logs the roots it adds. Policy only adds roots;
// Guard.Check's refusals apply to them as to the compiled ones.
func (r Roots) WithPolicy(goos string, machine, user []string) Roots {
	keep := func(scope string, entries []string) []string {
		var kept []string
		for _, raw := range entries {
			if err := checkRoot(goos, scope, raw); err != nil {
				slog.Warn("write roots policy entry dropped", "scope", scope, "entry", raw, "err", err)
				continue
			}
			kept = append(kept, raw)
		}
		return kept
	}
	machine, user = keep(ScopeMachine, machine), keep(ScopeUser, user)
	switch goos {
	case Darwin:
		r.Darwin = slices.Concat(r.Darwin, machine)
		r.DarwinUser = slices.Concat(r.DarwinUser, user)
	case Windows:
		r.Windows = slices.Concat(r.Windows, machine)
		r.WindowsUser = slices.Concat(r.WindowsUser, user)
	default:
		return r
	}
	if len(machine) > 0 || len(user) > 0 {
		slog.Info("write roots from managed policy", "machine", machine, "user", user)
	}
	return r
}

// checkRoot refuses a root that the guard could never use for scope on goos
// (Guard.expand): a machine root must expand to an absolute path, and a user
// root must stay inside the user's home, so start with the home variable
// (ProfileVar) or be relative.
func checkRoot(goos, scope, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("empty")
	}
	// Any absolute directory stands in for the home: only containment counts.
	home := filepath.Join(os.TempDir(), "sofcat-root-check-home")
	path, err := Guard{GOOS: goos, Vars: map[string]string{ProfileVar(goos): home}}.expand(raw, scope)
	if err != nil {
		return err
	}
	if scope == ScopeUser && !(Guard{GOOS: goos}).within(path, home) {
		return fmt.Errorf("user root %s is outside the user's home", raw)
	}
	return nil
}
