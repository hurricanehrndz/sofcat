package placement

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/render"
	"go.yaml.in/yaml/v4"
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

// itemsByPlacement maps each placement to the item_name of every item file
// under the fixture's items/ whose placements: lists it.
func itemsByPlacement(t *testing.T, dir string) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "items", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no item files under %s: %v", dir, err)
	}
	byPlacement := map[string][]string{}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var item struct {
			Name       string   `yaml:"item_name"`
			Placements []string `yaml:"placements"`
		}
		if err := yaml.Unmarshal(body, &item); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, name := range item.Placements {
			byPlacement[name] = append(byPlacement[name], item.Name)
		}
	}
	return byPlacement
}

// The harness smoke (test/manual/run-harness-smoke.sh) compares what the
// daemon writes on a Mac with each case's expected files, then, under
// harness_empty_manifest, with empty/expected: there only a rootless
// placement's files remain. This renders the same templates with the same
// facts on the host, so the expected files are known to be what the
// templates emit before a Mac is involved, byte for byte. It also holds the
// fixture to its shape: one item per placement with paths, none for a
// rootless one, every entry a darwin file, and each target file in one
// placement only.
func TestHarnessFixtureCases(t *testing.T) {
	table, dir := parseFixture(t, "harness")
	byPlacement := itemsByPlacement(t, dir)
	cases, err := filepath.Glob(filepath.Join(dir, "cases", "*"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no cases under %s: %v", dir, err)
	}
	for _, caseDir := range append(cases, filepath.Join(dir, "empty")) {
		placed := filepath.Base(caseDir) != "empty"
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
			written := 0
			for _, name := range table.Names() {
				spec := table.Placements[name]
				root, _ := spec.Path(Darwin)
				ctx := render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: spec.View(name, root)}
				items := byPlacement[name]
				if want := map[bool]int{true: 0, false: 1}[spec.Rootless()]; len(items) != want {
					t.Fatalf("placement %q: %d items under items/ list it, want %d", name, len(items), want)
				}
				if placed && len(items) == 1 {
					ctx.Items = []render.Item{{Name: items[0], Version: "1.0.0", Managed: true, Path: root + "/" + items[0]}}
				}
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
					switch {
					case got == nil && placed:
						t.Errorf("%s renders nothing with its item placed", out)
					case got == nil && !errors.Is(err, fs.ErrNotExist):
						t.Errorf("%s renders nothing, so the file is removed, but is expected (%v)", out, err)
					case got == nil:
					case err != nil:
						t.Fatal(err)
					case !bytes.Equal(got, want):
						t.Errorf("%s renders\n%s\nwant\n%s", out, got, want)
					default:
						written++
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
			if expected != written {
				t.Errorf("%d expected files for %d written placement files", expected, written)
			}
		})
	}
}
