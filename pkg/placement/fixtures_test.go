package placement

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// parseFixture parses the placements.yaml of a fixture under
// test/manual/fixtures, with its template files, and returns the fixture's
// directory too.
func parseFixture(t *testing.T, name string) (Table, string) {
	t.Helper()
	dir := filepath.Join("..", "..", "test", "manual", "fixtures", name)
	body, err := os.ReadFile(filepath.Join(dir, "placements.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := Parse(body, func(rel string) ([]byte, error) {
		return fs.ReadFile(os.DirFS(dir), rel)
	})
	if err != nil {
		t.Fatalf("%s fixture placements.yaml: %v", name, err)
	}
	return table, dir
}

// The harness smoke (test/manual/run-harness-smoke.sh) compares what the
// daemon writes on a Mac with each case's expected files. This renders the
// same templates with the same facts on the host, so the expected files are
// known to be what the templates emit before a Mac is involved, byte for
// byte. It also holds the fixture to its shape: one item per placement, every
// entry a guarded darwin file, and each target file in one placement only.
func TestHarnessFixtureCases(t *testing.T) {
	table, dir := parseFixture(t, "harness")
	cases, err := filepath.Glob(filepath.Join(dir, "cases", "*"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no cases under %s: %v", dir, err)
	}
	for _, caseDir := range cases {
		t.Run(filepath.Base(caseDir), func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(caseDir, "facts.json"))
			if err != nil {
				t.Fatal(err)
			}
			var facts conditions.Facts
			if err := json.Unmarshal(body, &facts); err != nil {
				t.Fatalf("facts.json: %v", err)
			}
			owner := map[string]string{}
			for _, name := range table.Names() {
				spec := table.Placements[name]
				root, _ := spec.Path(Darwin)
				ctx := render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: spec.View(name, root)}
				trees, _ := filepath.Glob(filepath.Join(dir, "rendered", "*", name, Darwin))
				if len(trees) != 1 {
					t.Fatalf("placement %q: %d items under rendered/, want 1", name, len(trees))
				}
				item := filepath.Base(filepath.Dir(filepath.Dir(trees[0])))
				ctx.Items = []render.Item{{Name: item, Version: "1.0.0", Managed: true, Path: root + "/" + item}}
				for _, f := range spec.Files {
					out := f.Paths[Darwin]
					if out == "" {
						t.Fatalf("placement %q: a files entry has no darwin path", name)
					}
					if other, ok := owner[out]; ok {
						t.Fatalf("%s belongs to both %q and %q", out, other, name)
					}
					owner[out] = name
					tmpl, err := render.Parse(out, f.Template)
					if err != nil {
						t.Fatal(err)
					}
					got, _, err := tmpl.Render(ctx, facts, out)
					if err != nil {
						t.Fatal(err)
					}
					want, err := os.ReadFile(filepath.Join(caseDir, "expected", filepath.FromSlash(out)))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want) {
						t.Errorf("%s renders\n%s\nwant\n%s", out, got, want)
					}
					empty := ctx
					empty.Items = nil
					if got, _, err := tmpl.Render(empty, facts, out); err != nil || got != nil {
						t.Errorf("%s with nothing placed renders %q, %v; want nothing, so the file is removed", out, got, err)
					}
				}
			}
			var expected int
			_ = filepath.WalkDir(filepath.Join(caseDir, "expected"), func(_ string, d fs.DirEntry, _ error) error {
				if d != nil && !d.IsDir() {
					expected++
				}
				return nil
			})
			if expected != len(owner) {
				t.Errorf("%d expected files for %d placement files", expected, len(owner))
			}
		})
	}
}
