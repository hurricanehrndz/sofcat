package placement

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/harness"
)

// TestGuard covers R7: a placement is written only inside a write root for
// its scope, never inside the data directory, and never over or around the
// directories holding sofcat's binaries, whatever symlinks say.
func TestGuard(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	root2 := filepath.Join(base, "root2")
	data := filepath.Join(root, "data")
	bin := filepath.Join(root, "app", "bin")
	for _, dir := range []string{root, root2, outside, data, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A symlink inside the root that leads out of it.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	// A dangling symlink: whatever it names would be created by a write.
	if err := os.Symlink(filepath.Join(outside, "nothing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}

	g := Guard{
		GOOS:       harness.Darwin,
		Roots:      Roots{Darwin: []string{root, root2}},
		DataDir:    data,
		BinaryDirs: []string{bin},
	}
	cases := []struct {
		name, path string
		refused    bool
	}{
		{"inside a root, not yet created", filepath.Join(root, "skills", "new"), false},
		{"the root itself", root2, false},
		{"a root that contains a binary directory", root, true},
		{"outside every root", filepath.Join(outside, "skills"), true},
		{"symlink escape", filepath.Join(root, "escape", "skills"), true},
		{"dangling symlink", filepath.Join(root, "dangling", "skills"), true},
		{"dot-dot escape", root + "/skills/../../outside", true},
		{"inside the data directory", filepath.Join(data, "skills"), true},
		{"inside a binary directory", filepath.Join(bin, "skills"), true},
		{"equal to a binary directory", bin, true},
		{"containing a binary directory", filepath.Join(root, "app"), true},
		{"relative path", "skills", true},
		{"unexpanded variable", "${USERPROFILE}/skills", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := g.Resolve(tc.path, harness.ScopeMachine)
			if tc.refused != (err != nil) {
				t.Fatalf("Resolve(%s) err = %v, want refused %v", tc.path, err, tc.refused)
			}
			if err != nil && !errors.Is(err, ErrRefused) {
				t.Fatalf("error does not wrap ErrRefused: %v", err)
			}
		})
	}

	// User scope has no roots unless the build gives some.
	if _, err := g.Resolve(filepath.Join(root, "skills"), harness.ScopeUser); err == nil {
		t.Error("user scope accepted with no user roots")
	}
}

// TestGuardCase: on Windows and (by default) macOS, paths differing only in
// case are the same directory, so a binary directory cannot be reached by
// changing case.
func TestGuardCase(t *testing.T) {
	base := t.TempDir()
	for _, goos := range []string{harness.Windows, harness.Darwin} {
		g := Guard{GOOS: goos}
		if !g.within(filepath.Join(base, "ROOT", "x"), filepath.Join(base, "root")) {
			t.Errorf("%s comparison is case-sensitive", goos)
		}
	}

	root := filepath.Join(base, "root")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	g := Guard{GOOS: harness.Darwin, Roots: Roots{Darwin: []string{root}}, BinaryDirs: []string{bin}}
	if _, err := g.Resolve(filepath.Join(root, "BIN", "skills"), harness.ScopeMachine); !errors.Is(err, ErrRefused) {
		t.Errorf("placement in the binary directory under another case accepted: %v", err)
	}
}

// TestGuardUserProfile: ${USERPROFILE} expands only for user scope and only
// from the caller's value, against profile-relative user roots.
func TestGuardUserProfile(t *testing.T) {
	profile := t.TempDir()
	g := Guard{
		GOOS:  harness.Darwin, // POSIX paths on the test host; scope logic is the same
		Roots: Roots{Darwin: []string{profile}},
		Vars:  map[string]string{"USERPROFILE": profile},
	}
	if _, err := g.Resolve("${USERPROFILE}/skills", harness.ScopeMachine); err == nil {
		t.Error("${USERPROFILE} expanded in machine scope")
	}
	g.GOOS = harness.Windows
	g.Roots = Roots{WindowsUser: []string{".agents"}}
	if _, err := g.Resolve(filepath.Join("${USERPROFILE}", ".agents", "skills"), harness.ScopeUser); err != nil {
		t.Errorf("user placement under a profile-relative root refused: %v", err)
	}
	if _, err := g.Resolve(filepath.Join("${USERPROFILE}", "Documents"), harness.ScopeUser); err == nil {
		t.Error("user placement outside the user roots accepted")
	}
}

func TestParseRoots(t *testing.T) {
	r, err := ParseRoots([]byte("darwin: [/a]\nwindows: ['${ProgramFiles}\\X']\nwindows_user: [.agents]\n"))
	if err != nil || len(r.Darwin) != 1 || len(r.Windows) != 1 || len(r.WindowsUser) != 1 {
		t.Fatalf("ParseRoots = %+v, %v", r, err)
	}
	if _, err := ParseRoots([]byte("linux: [/a]\n")); err == nil {
		t.Error("unknown key accepted")
	}
}
