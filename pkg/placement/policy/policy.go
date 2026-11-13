// Package policy reads the write roots managed policy adds to a build's
// compiled ones: a registry key on Windows, a managed preferences plist on
// macOS. It is apart from package placement so that makecatalogs, which
// parses placement tables, links no registry code.
package policy

import (
	"log/slog"
	"reflect"
	"sync"

	"github.com/micromdm/plist"

	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// current is the process's snapshot of the effective roots: read at first
// use, and again only by Refresh. The service refreshes it at the start of
// every run; the per-user helper, one process per sync, never does.
var (
	mu      sync.Mutex
	current *placement.Roots
	// readRoots is a variable so tests can supply policy on any host.
	readRoots = readPolicyRoots
)

// EffectiveRoots returns the compiled roots plus the valid write roots of
// managed policy for this OS, as last read. Missing, unreadable or malformed
// policy adds none.
func EffectiveRoots() placement.Roots {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		read()
	}
	return *current
}

// Refresh reads the policy again and returns the roots, which EffectiveRoots
// then returns until the next Refresh. A root the policy no longer has is
// gone from them: placements under it are refused from then on, and what was
// placed there stays.
func Refresh() placement.Roots {
	mu.Lock()
	defer mu.Unlock()
	read()
	return *current
}

// read replaces the snapshot, with mu held, and logs the host's roots at the
// first read and whenever they differ from the last one's.
func read() {
	machine, user := readRoots()
	next := placement.Compiled().WithPolicy(placement.HostOS(), machine, user)
	if current == nil || !reflect.DeepEqual(*current, next) {
		slog.Info("effective write roots",
			"machine", next.For(placement.HostOS(), placement.ScopeMachine),
			"user", next.For(placement.HostOS(), placement.ScopeUser))
	}
	current = &next
}

// parsePlist reads the WriteRoots dict of a managed preferences plist, binary
// or XML: its Machine and User arrays of strings. A plist without the dict has
// no roots. An entry or array of the wrong type is dropped with a warning; the
// rest are kept.
func parsePlist(data []byte) (machine, user []string, err error) {
	var doc struct {
		WriteRoots map[string]any `plist:"WriteRoots"`
	}
	if err := plist.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	strs := func(name string) []string {
		v, ok := doc.WriteRoots[name]
		if !ok {
			return nil
		}
		entries, ok := v.([]any)
		if !ok {
			slog.Warn("write roots policy value dropped", "name", name, "err", "not an array")
			return nil
		}
		var out []string
		for _, e := range entries {
			s, ok := e.(string)
			if !ok {
				slog.Warn("write roots policy entry dropped", "name", name, "entry", e, "err", "not a string")
				continue
			}
			out = append(out, s)
		}
		return out
	}
	return strs("Machine"), strs("User"), nil
}
