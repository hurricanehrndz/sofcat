package placement

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestWithPolicy: policy only adds roots for this OS, and an entry the guard
// could not use (relative machine root, user root outside the home, a
// variable this scope lacks) is dropped while its neighbours are kept.
func TestWithPolicy(t *testing.T) {
	// Absolute on the host: the guard expands with the host's path rules.
	abs := filepath.Join(t.TempDir(), "codex")
	compiled := Roots{Darwin: []string{"/Library/Application Support/SofCat"}, Windows: []string{`${ProgramData}\SofCat`}}
	got := compiled.WithPolicy(Darwin,
		[]string{abs, "codex", "${ProgramFiles}/X", "${HOME}/x", ""},
		[]string{"${HOME}/.claude", ".agents", abs, "${HOME}/../other", "${HOME}x/y"})
	want := Roots{
		Darwin:     []string{"/Library/Application Support/SofCat", abs},
		Windows:    []string{`${ProgramData}\SofCat`},
		DarwinUser: []string{"${HOME}/.claude", ".agents"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WithPolicy = %+v, want %+v", got, want)
	}
	if len(compiled.Darwin) != 1 {
		t.Errorf("compiled roots changed: %v", compiled.Darwin)
	}
	if got := compiled.WithPolicy(Darwin, nil, nil); !reflect.DeepEqual(got, compiled) {
		t.Errorf("no policy: %+v, want the compiled roots", got)
	}
}

// TestGuardPolicyRoot: a policy root lets a stock build write where its
// compiled roots do not, but never into Data or over its binaries.
func TestGuardPolicyRoot(t *testing.T) {
	base := t.TempDir()
	policy := filepath.Join(base, "policy")
	data := filepath.Join(policy, "data")
	bin := filepath.Join(policy, "bin")
	g := Guard{
		GOOS:       Darwin,
		Roots:      Roots{Darwin: []string{filepath.Join(base, "stock")}}.WithPolicy(Darwin, []string{policy}, nil),
		DataDir:    data,
		BinaryDirs: []string{bin},
	}
	if _, err := g.Resolve(filepath.Join(policy, "settings"), ScopeMachine); err != nil {
		t.Errorf("a path under the policy root refused: %v", err)
	}
	for name, path := range map[string]string{
		"inside Data":         filepath.Join(data, "x"),
		"inside a binary dir": filepath.Join(bin, "x"),
		"outside every root":  filepath.Join(base, "elsewhere"),
	} {
		if _, err := g.Resolve(path, ScopeMachine); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: error %v, want refused", name, err)
		}
	}
	// A machine policy root grants nothing in user scope.
	g.Vars = map[string]string{"HOME": base}
	if _, err := g.Resolve(filepath.Join(policy, "x"), ScopeUser); err == nil || !strings.Contains(err.Error(), "user scope") {
		t.Errorf("a user-scope path under a machine policy root: %v, want refused", err)
	}
}
