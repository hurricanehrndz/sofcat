package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeGoldenTree writes the fixed tree the golden values below pin: nested
// directories, an executable file, a plain file and an empty directory. The
// file names sort differently by slash path ("a-b" < "a/b") than a walk visits
// them, so the order is pinned too.
func writeGoldenTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := []struct {
		path string
		body string
		mode os.FileMode
	}{
		{"SKILL.md", "# Hello\n", 0o644},
		{"a/b/c.txt", "nested\n", 0o644},
		{"a-b.txt", "dash\n", 0o644},
		{"scripts/run.sh", "#!/bin/sh\necho hi\n", 0o755},
	}
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, f.mode); err != nil { // ignore the umask
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The tree hash and the zip bytes are a published format: catalogs carry them
// and agents recompute them. Any change to either definition must fail here.
func TestGolden(t *testing.T) {
	tests := []struct {
		goos     string
		treeHash string
		zipHash  string
	}{
		{Darwin, "sha256:a28ab37cb6178d8848e0251779a4565e105ed5bb0d520853cf5596d88c030745", "20e8177a6904ba91c76bd8b4c10bd1e7019722d1e101a12719f1f46d16e5a764"},
		{Windows, "sha256:212d74340b270417035f174b8e8bcd1b0ba20234d377ee3835ec24608a8c2741", "d55943a0c7bb1ed674ae9b5b8ea170e6c37b1597db83c7e91d650faa77082ee2"},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			if tc.goos == Darwin && runtime.GOOS == "windows" {
				t.Skip("Windows files have no executable bit to keep")
			}
			dir := writeGoldenTree(t)

			got, err := TreeHash(dir, tc.goos)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.treeHash {
				t.Errorf("tree hash %s, want %s", got, tc.treeHash)
			}

			tree, err := Read(dir, tc.goos)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			packed, err := tree.WriteZip(&buf)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(buf.Bytes())
			if hex.EncodeToString(sum[:]) != tc.zipHash || packed.Hash != tc.zipHash {
				t.Errorf("zip hash %x (reported %s), want %s", sum, packed.Hash, tc.zipHash)
			}
			if packed.TreeHash != tc.treeHash {
				t.Errorf("packed tree hash %s, want %s", packed.TreeHash, tc.treeHash)
			}
		})
	}
}

func TestRefusesSymlinkAndEmptyTree(t *testing.T) {
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "only", "dirs"), 0o755); err != nil {
		t.Fatal(err)
	}

	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("SKILL.md", filepath.Join(linked, "link.md")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	for name, dir := range map[string]string{"empty": empty, "symlink": linked} {
		t.Run(name, func(t *testing.T) {
			if _, err := TreeHash(dir, Darwin); err == nil {
				t.Error("TreeHash accepted the tree")
			}
			out := filepath.Join(t.TempDir(), "out.zip")
			if _, err := PackFile(dir, Darwin, out); err == nil {
				t.Error("PackFile accepted the tree")
			}
			if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 0 {
				t.Errorf("PackFile left %d file(s) behind", len(entries))
			}
		})
	}
}

// Windows has no executable bit, so a windows target records the same tree
// as all 644 whatever OS computes it.
func TestWindowsTargetIgnoresExecBit(t *testing.T) {
	dir := writeGoldenTree(t)
	tree, err := Read(dir, Windows)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range tree.files {
		if f.exec {
			t.Errorf("%s recorded as executable", f.path)
		}
	}
}

// Templates names the .tmpl files of a tree and refuses the shapes a render
// could not place: a bare .tmpl and a template of a template; for a tree as
// shipped, ShippedTemplates also refuses a template beside the file it would
// overwrite, which a placed tree holds by design.
func TestTemplates(t *testing.T) {
	shipped := []string{".claude-plugin/plugin.json.tmpl", "NOTE.md.tmpl", "SKILL.md", "sub/x.tmpl"}
	got, err := ShippedTemplates(shipped)
	if err != nil || len(got) != 3 || got[0] != ".claude-plugin/plugin.json.tmpl" || got[2] != "sub/x.tmpl" {
		t.Fatalf("ShippedTemplates = %v, %v", got, err)
	}
	for name, paths := range map[string][]string{
		"bare .tmpl":          {"SKILL.md", ".tmpl"},
		"bare .tmpl in a dir": {"sub/.tmpl"},
		"x.tmpl.tmpl":         {"x.tmpl.tmpl"},
	} {
		if got, err := Templates(paths); err == nil {
			t.Errorf("%s: Templates(%v) = %v, want an error", name, paths, got)
		}
	}
	placed := []string{"a/x", "a/x.tmpl"}
	if got, err := ShippedTemplates(placed); err == nil {
		t.Errorf("ShippedTemplates(%v) = %v, want an error", placed, got)
	}
	// A directory x beside x.tmpl: the render could not write x at all.
	for _, paths := range [][]string{{"a/x/y", "a/x.tmpl"}, {"x/sub/y", "x.tmpl", "z"}} {
		if got, err := ShippedTemplates(paths); err == nil || !strings.Contains(err.Error(), "ships a directory") {
			t.Errorf("ShippedTemplates(%v) = %v, %v; want a directory refusal", paths, got, err)
		}
		if got, err := Templates(paths); err != nil || len(got) != 1 {
			t.Errorf("Templates(%v) = %v, %v; want the template alone", paths, got, err)
		}
	}
	if got, err := Templates(placed); err != nil || len(got) != 1 {
		t.Errorf("Templates(%v) = %v, %v; want the template beside its output", placed, got, err)
	}
}
