package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// The value builders let a template build one value and encode it once, so
// it needs no hand-placed commas; they follow sprig's argument order.
func TestValueBuilders(t *testing.T) {
	vars := map[string]any{"name": "acme", "empty": ""}
	for source, want := range map[string]string{
		`{{ json (dict "b" 1 "a" (list "x" 2)) }}`: `{"a":["x",2],"b":1}`,
		`{{ json (dict) }}`:                        `{}`,
		`{{ json (list) }}`:                        `[]`,
		`{{ $d := dict }}{{ range .L }}{{ $_ := set $d . true }}{{ end }}{{ json $d }}`:          `{"p":true,"q":true}`,
		`{{ $l := list }}{{ range .L }}{{ $l = append $l (dict "n" .) }}{{ end }}{{ json $l }}`:  `[{"n":"p"},{"n":"q"}]`,
		`{{ json (append .L "r") }} {{ json .L }}`:                                               `["p","q","r"] ["p","q"]`,
		`{{ default "d" .V.empty }} {{ default "d" .V.name }} {{ default "d" (index .V "no") }}`: `d acme d`,
		`{{ hasKey .V "empty" }} {{ hasKey .V "no" }}`:                                           `true false`,
	} {
		if got := mustRender(t, source, map[string]any{"V": vars, "L": []string{"p", "q"}}, "out.txt"); got != want {
			t.Errorf("%s = %q, want %q", source, got, want)
		}
	}
	// set changes a map in place, so it takes only a dict of this render:
	// the placement's vars and a fact's value are shared with later renders.
	for source, want := range map[string]string{
		`{{ set .V "k" 1 }}`:            "not a dict this template built",
		`{{ set (fact "site") "k" 1 }}`: "not a dict this template built",
		`{{ dict "a" }}`:                "a key without a value",
		`{{ dict 1 2 }}`:                "not a string",
		`{{ append "x" 1 }}`:            "not a list",
	} {
		if _, _, err := render(t, source, map[string]any{"V": vars}, "out.txt"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", source, err, want)
		}
	}
	if _, ok := vars["k"]; ok {
		t.Error("set changed the vars")
	}
}

const treeTemplate = `{{ range .Items }}{{ if .HasFile "a/plugin.json" }}{{ (.ReadJSON "a/plugin.json").interface.category }} {{ (.ReadJSON "a/plugin.json").n }}{{ else }}none{{ end }}{{ end }}`

// An item's methods read its tree: a catalog template lists a plugin by what
// its tree ships instead of by a hand-kept map that can drift from it.
func TestItemTree(t *testing.T) {
	tree := fstest.MapFS{
		"a/plugin.json": {Data: []byte(`{"interface": {"category": "dev"}, "n": 3.0}`)},
		"bad.json":      {Data: []byte(`{} {}`)},
		"big.json":      {Data: []byte(`"` + strings.Repeat("x", MaxTreeFile) + `"`)},
	}
	ctx := FileContext{Items: []Item{Item{Name: "p"}.WithTree(tree)}}
	if got := mustRender(t, treeTemplate, ctx, "out.txt"); got != "dev 3" {
		t.Errorf("tree = %q", got)
	}
	for source, want := range map[string]string{
		`{{ range .Items }}{{ .HasFile "a" }} {{ .HasFile "no/such" }}{{ end }}`: "false false",
	} {
		if got := mustRender(t, source, ctx, "out.txt"); got != want {
			t.Errorf("%s = %q, want %q", source, got, want)
		}
	}
	for source, want := range map[string]string{
		`{{ range .Items }}{{ .ReadJSON "no.json" }}{{ end }}`:    `item "p": "no.json"`,
		`{{ range .Items }}{{ .ReadJSON "bad.json" }}{{ end }}`:   "data after the first value",
		`{{ range .Items }}{{ .ReadJSON "big.json" }}{{ end }}`:   "limit",
		`{{ range .Items }}{{ .HasFile "../x" }}{{ end }}`:        "not a slash-separated path inside the tree",
		`{{ range .Items }}{{ .HasFile "/etc/passwd" }}{{ end }}`: "not a slash-separated path inside the tree",
	} {
		if _, _, err := render(t, source, ctx, "out.txt"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", source, err, want)
		}
	}
}

// Without WithTree the tree is the directory at Path, as on a device. The
// service reads it as root or SYSTEM, so a link out of the tree must not be
// followed; a tree that is gone has no files.
func TestItemTreeOnDisk(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "a", "plugin.json"), []byte(`{"interface": {"category": "dev"}, "n": 3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.json"), []byte(`"secret"`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := FileContext{Items: []Item{{Name: "p", Path: tree}}}
	if got := mustRender(t, treeTemplate, ctx, "out.txt"); got != "dev 3" {
		t.Errorf("tree = %q", got)
	}
	gone := FileContext{Items: []Item{{Name: "p", Path: filepath.Join(dir, "gone")}}}
	if got := mustRender(t, treeTemplate, gone, "out.txt"); got != "none" {
		t.Errorf("missing tree = %q", got)
	}
	if err := os.Symlink(filepath.Join(dir, "secret.json"), filepath.Join(tree, "link.json")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	for _, source := range []string{`{{ range .Items }}{{ .ReadJSON "link.json" }}{{ end }}`, `{{ range .Items }}{{ .HasFile "link.json" }}{{ end }}`} {
		out, _, err := render(t, source, ctx, "out.txt")
		if err == nil {
			t.Errorf("%s followed a link out of the tree: %q", source, out)
		}
	}
}
