//go:build windows

package installer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// A directory junction below the home is refused like a symlink, and needs
// no privilege to make.
func TestMergeRefusesJunctionedParent(t *testing.T) {
	m := newMergeTest(t)
	m.write(t, `{}`)
	dir := filepath.Dir(m.settings)
	target := filepath.Join(filepath.Dir(dir), "dotfiles")
	if err := os.Rename(dir, target); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", dir, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	failed := m.sync(t, "a")
	if len(failed) != 1 || !errors.Is(failed[0].Err, placement.ErrRefused) {
		t.Fatalf("sync = %+v, want one refusal", failed)
	}
	if got := readFile(t, filepath.Join(target, "settings.json")); got != `{}` {
		t.Errorf("settings through the junction = %q, want {}", got)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 1 {
		t.Errorf("junction target holds %v (%v), want only settings.json", entries, err)
	}
	if exists(m.record) {
		t.Error("a refused merge wrote the record")
	}
}
