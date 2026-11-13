package policy

import (
	"slices"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// machineRoots are the host's machine roots in r.
func machineRoots(r placement.Roots) []string {
	return r.For(placement.HostOS(), placement.ScopeMachine)
}

// resetSnapshot makes the next EffectiveRoots read as a new process would.
func resetSnapshot(t *testing.T) {
	t.Helper()
	mu.Lock()
	current = nil
	mu.Unlock()
}

// TestRefresh: the service reads the policy once per run, not once per
// process. A root written after the first read must apply from the next
// Refresh, and one removed must stop admitting placements there, while
// everything between two refreshes sees one snapshot.
func TestRefresh(t *testing.T) {
	root := map[string]string{placement.Darwin: "/opt/policy", placement.Windows: `C:\policy`}[placement.HostOS()]
	if root == "" {
		t.Skip("no policy store on this OS")
	}
	var machine []string
	readRoots = func() ([]string, []string) { return machine, nil }
	resetSnapshot(t)
	t.Cleanup(func() {
		readRoots = readPolicyRoots
		resetSnapshot(t)
	})

	if got := machineRoots(EffectiveRoots()); slices.Contains(got, root) {
		t.Fatalf("roots without policy: %q", got)
	}
	machine = []string{root}
	if got := machineRoots(EffectiveRoots()); slices.Contains(got, root) {
		t.Fatalf("a policy change applied before the next refresh: %q", got)
	}
	if got := machineRoots(Refresh()); !slices.Contains(got, root) {
		t.Fatalf("policy written after the first read did not apply: %q", got)
	}
	if got := machineRoots(EffectiveRoots()); !slices.Contains(got, root) {
		t.Fatalf("EffectiveRoots does not return the refreshed roots: %q", got)
	}
	machine = nil
	if got := machineRoots(Refresh()); slices.Contains(got, root) {
		t.Fatalf("policy removed, root kept: %q", got)
	}
}
