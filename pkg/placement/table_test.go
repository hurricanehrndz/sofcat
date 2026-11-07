package placement

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// The sample table under examples/ is documentation; it must stay valid and
// show the optional fields.
func TestExampleTableParses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "examples", "placements.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	var files, itemsDir, vars bool
	for _, p := range table.Placements {
		files = files || len(p.Files) > 0
		itemsDir = itemsDir || p.ItemsDir != ""
		vars = vars || p.Vars != nil
	}
	if !files || !itemsDir || !vars {
		t.Errorf("example shows files=%v items_dir=%v vars=%v, want all", files, itemsDir, vars)
	}
}

func TestParse(t *testing.T) {
	table, err := Parse([]byte(`
check_facts: {site: east, floors: [1, 2]}
placements:
  codex-skills:
    paths:
      darwin: /etc/codex/skills
      windows: ${USERPROFILE}\.agents\skills
    scope: {windows: user}
  market:
    paths: {darwin: /m, windows: 'C:\m'}
    items_dir: plugins
    vars:
      name: acme
      owner: {name: Acme IT}
    files:
      - path: .claude-plugin/marketplace.json
        template: '{"name": {{ json .Placement.Vars.name }}}'
      - path: catalog.toml
        template: x
      - paths: {windows: 'C:\drop-in.json'}
        template: '{{ if .Items }}{}{{ end }}'
`))
	if err != nil {
		t.Fatal(err)
	}
	codex := table.Placements["codex-skills"]
	if codex.ScopeFor(Darwin) != ScopeMachine || codex.ScopeFor(Windows) != ScopeUser {
		t.Errorf("scopes darwin=%s windows=%s", codex.ScopeFor(Darwin), codex.ScopeFor(Windows))
	}
	if _, ok := codex.Path(Windows); !ok {
		t.Error("no windows path")
	}
	market := table.Placements["market"]
	want := []File{
		{Path: ".claude-plugin/marketplace.json", Template: `{"name": {{ json .Placement.Vars.name }}}`},
		{Path: "catalog.toml", Template: "x"},
		{Paths: map[string]string{Windows: `C:\drop-in.json`}, Template: "{{ if .Items }}{}{{ end }}"},
	}
	if market.ItemsDir != "plugins" || !reflect.DeepEqual(market.Files, want) {
		t.Errorf("market = %+v", market)
	}
	if got := table.Names(); !reflect.DeepEqual(got, []string{"codex-skills", "market"}) {
		t.Errorf("names %v", got)
	}
	if table.CheckFacts["site"] != "east" || len(table.CheckFacts) != 2 {
		t.Errorf("check_facts = %v", table.CheckFacts)
	}
	if empty, err := Parse(nil); err != nil || len(empty.Placements) != 0 {
		t.Errorf("empty file: %v, %v", empty, err)
	}
}

// Templates read vars as JSON values, under .Placement.Vars, whatever file
// they render; Root is always the placement root.
func TestView(t *testing.T) {
	p := Spec{Vars: map[string]any{"name": "acme", "owner": map[string]any{"name": "Acme IT"}}}
	want := map[string]any{"name": "acme", "owner": map[string]any{"name": "Acme IT"}}
	if v := p.View("market", "/m"); v.Name != "market" || v.Root != "/m" || !reflect.DeepEqual(v.Vars, want) {
		t.Errorf("view = %+v", v)
	}
}

// Action labels and "Applies to" follow table order, which is the
// order the author wrote, not the alphabet.
func TestNamesKeepFileOrder(t *testing.T) {
	table, err := Parse([]byte(`
placements:
  zed: {paths: {darwin: /z}}
  alpha: {paths: {darwin: /a}}
  mid: {paths: {darwin: /m}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := table.Names(); !reflect.DeepEqual(got, []string{"zed", "alpha", "mid"}) {
		t.Errorf("names %v, want file order", got)
	}

	// A duplicate key would leave Order and Placements disagreeing.
	_, err = Parse([]byte(`
placements:
  p: {paths: {darwin: /a}}
  p: {paths: {darwin: /b}}
`))
	if err == nil || !strings.Contains(err.Error(), `"p"`) {
		t.Errorf("duplicate placement: error %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	tests := map[string]struct{ yaml, want string }{
		"unknown field": {`
placements:
  p: {paths: {darwin: /p}, colour: red}`, "colour"},
		"unknown top-level field": {"placement: {}\n", "placement"},
		"no paths": {`
placements:
  p: {exclusive: true}`, "no paths"},
		"unknown path OS": {`
placements:
  p: {paths: {linux: /p}}`, "unknown OS"},
		"darwin user scope": {`
placements:
  p: {paths: {darwin: /p}, scope: {darwin: user}}`, "darwin: user"},
		"bad scope value": {`
placements:
  p: {paths: {windows: 'C:\p'}, scope: {windows: everyone}}`, "want machine or user"},
		"USERPROFILE in machine scope": {`
placements:
  p: {paths: {windows: '${USERPROFILE}\skills'}}`, "${USERPROFILE}"},
		"unknown action label": {`
placements:
  p: {paths: {darwin: /p}, action_labels: {buy: Buy}}`, "action_labels"},
		"name with a path": {`
placements:
  a/b: {paths: {darwin: /p}}`, "invalid name"},
		"vars that is not a mapping": {`
placements:
  p: {paths: {darwin: /p}, vars: [a, b]}`, "!!seq into map"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// The old placement kinds are gone, not ignored: a table written for them
// fails to load rather than placing items somewhere else.
func TestParseRejectsRemovedFields(t *testing.T) {
	tests := map[string]string{
		"kind":               `p: {kind: directory, paths: {darwin: /p}}`,
		"marketplace":        `p: {paths: {darwin: /p}, marketplace: {name: a, owner: b}}`,
		"registers":          `p: {paths: {darwin: /p}, registers: q}`,
		"template":           `p: {paths: {darwin: /p}, template: x}`,
		"bare file path":     `p: {paths: {darwin: /p}, files: [.x.json]}`,
		"manifest_files":     `p: {paths: {darwin: /p}, manifest_files: [.x.json]}`,
		"unknown file field": `p: {paths: {darwin: /p}, files: [{path: .x, template: x, mode: 644}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte("placements:\n  " + body + "\n")); err == nil {
				t.Fatal("parsed")
			}
		})
	}
}

// items_dir is a directory under the root, never above it, and never one
// a relative files path could also name.
func TestParseItemsDir(t *testing.T) {
	for _, dir := range []string{"plugins", "a/b"} {
		if _, err := Parse([]byte("placements:\n  p: {paths: {darwin: /p}, items_dir: '" + dir + "'}\n")); err != nil {
			t.Errorf("items_dir %q: %v", dir, err)
		}
	}
	for _, dir := range []string{"../x", "/abs", `a\b`, "a/../b", "a/", ".", ".hidden", "./plugins", "con", "plugins.", "a:b"} {
		_, err := Parse([]byte("placements:\n  p: {paths: {darwin: /p}, items_dir: '" + dir + "'}\n"))
		if err == nil || !strings.Contains(err.Error(), "items_dir") {
			t.Errorf("items_dir %q: error %v", dir, err)
		}
	}
}

// Files and items_dir are machine scope only, since the per-user helper
// rebuilds a user spec without them.
func TestParseUserScopeLimits(t *testing.T) {
	for name, extra := range map[string]string{
		"items_dir": "items_dir: plugins",
		"files":     "files: [{path: .x.json, template: x}]",
		"vars":      "vars: {name: x}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte("placements:\n  p: {paths: {windows: '${USERPROFILE}\\p'}, scope: {windows: user}, " + extra + "}\n"))
			if err == nil || !strings.Contains(err.Error(), "need machine scope") {
				t.Errorf("error %v", err)
			}
		})
	}
}

// Templates are checked at load, so a repository fails -check rather than
// every device failing the placement.
func TestParseRejectsFiles(t *testing.T) {
	tests := map[string]struct{ placement, want string }{
		"path and paths": {
			`{paths: {darwin: /p}, files: [{path: .x, paths: {darwin: /x}, template: x}]}`, "files[0]: set path or paths, not both",
		},
		"neither path nor paths": {
			`{paths: {darwin: /p}, files: [{template: x}]}`, "files[0]: needs path or paths",
		},
		"empty paths": {
			`{paths: {darwin: /p}, files: [{paths: {}, template: x}]}`, "files[0]: needs path or paths",
		},
		"no template": {
			`{paths: {darwin: /p}, files: [{path: .x}]}`, "files[0]: needs a template",
		},
		"template that does not parse": {
			`{paths: {darwin: /p}, files: [{path: .x, template: '{{ .Items'}]}`, "files[0]",
		},
		"template calling now()": {
			`{paths: {darwin: /p}, files: [{path: .x, template: '{{ cond "now() > date(\"2020-01-01\")" }}'}]}`, "now()",
		},
		"path outside the root": {
			`{paths: {darwin: /p}, files: [{path: ../x.json, template: x}]}`, "not a clean relative path",
		},
		"absolute path": {
			`{paths: {darwin: /p}, files: [{path: /x.json, template: x}]}`, "not a clean relative path",
		},
		"backslash path": {
			`{paths: {darwin: /p}, files: [{path: '.a\x.json', template: x}]}`, "not a clean relative path",
		},
		"unclean path": {
			`{paths: {darwin: /p}, files: [{path: .a//x.json, template: x}]}`, "not a clean relative path",
		},
		"path listed twice": {
			`{paths: {darwin: /p}, files: [{path: .x.json, template: x}, {path: .x.json, template: y}]}`, `files[1]: path ".x.json" is listed twice`,
		},
		"path where an item tree could be": {
			`{paths: {darwin: /p}, files: [{path: catalog.json, template: x}]}`, "must start with a . segment",
		},
		"path under items_dir": {
			`{paths: {darwin: /p}, items_dir: plugins/all, files: [{path: plugins/x.json, template: x}]}`, `is under items_dir "plugins"`,
		},
		"paths on an OS the placement lacks": {
			`{paths: {darwin: /p}, files: [{paths: {windows: 'C:\x.json'}, template: x}]}`, "files[0]: paths: windows, which the placement has no path for",
		},
		"paths with an unknown OS": {
			`{paths: {darwin: /p}, files: [{paths: {linux: /x.json}, template: x}]}`, `files[0]: paths: unknown OS "linux"`,
		},
		"paths with an empty path": {
			`{paths: {darwin: /p}, files: [{paths: {darwin: ''}, template: x}]}`, "files[0]: paths: empty path for darwin",
		},
		"paths with USERPROFILE": {
			`{paths: {windows: 'C:\p'}, files: [{paths: {windows: '${USERPROFILE}\x.json'}, template: x}]}`, "${USERPROFILE}",
		},
		"path under items_dir in another case": {
			`{paths: {darwin: /p}, items_dir: plugins, files: [{path: Plugins/x.json, template: x}]}`, `is under items_dir "plugins"`,
		},
		"path with a trailing dot Windows drops": {
			`{paths: {darwin: /p}, items_dir: plugins, files: [{path: plugins./x.json, template: x}]}`, "not a clean relative path",
		},
		"path with a Windows reserved name": {
			`{paths: {darwin: /p}, files: [{path: .a/con/x.json, template: x}]}`, "not a clean relative path",
		},
		"path with a colon": {
			`{paths: {darwin: /p}, files: [{path: ".a:b", template: x}]}`, "not a clean relative path",
		},
		"path over sofcat's own files": {
			`{paths: {darwin: /p}, files: [{path: .SOFCAT-owned, template: x}]}`, "sofcat keeps for its own files",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte("placements:\n  p: " + tc.placement + "\n"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}

// What a files entry may name: a relative path beside items_dir, a
// dot-path without it, and per-OS paths on a subset of the placement's OSes.
func TestParseAcceptsFiles(t *testing.T) {
	tests := map[string]string{
		"dot path without items_dir":   `{paths: {darwin: /p}, files: [{path: .claude-plugin/marketplace.json, template: x}]}`,
		"any path beside items_dir":    `{paths: {darwin: /p}, items_dir: plugins, files: [{path: catalog.toml, template: x}, {path: plugins.json, template: x}]}`,
		"per-OS paths on a subset":     `{paths: {darwin: /p, windows: 'C:\p'}, files: [{paths: {darwin: /etc/x.json}, template: x}]}`,
		"same path, relative and OS'd": `{paths: {darwin: /p}, files: [{path: .x, template: x}, {paths: {darwin: /p/.x}, template: y}]}`,
	}
	for name, placement := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte("placements:\n  p: " + placement + "\n")); err != nil {
				t.Error(err)
			}
		})
	}
}

// Labels resolve item first, then the first placement in table order (not
// variant or map order) that sets the key; applies_to is the ordered union.
// A placement without a variant on this OS contributes nothing.
func TestStorefront(t *testing.T) {
	table, err := Parse([]byte(`
placements:
  skills:
    paths: {windows: 'C:\skills'}
    applies_to: [Claude Code]
    action_labels: {install: Add, installing: Adding, installed: Added, applies_to: Works in}
  market:
    paths: {windows: 'C:\market'}
    applies_to: [Claude Code, Codex]
    action_labels: {install: Make available, installing: Making available, remove: Withdraw, pending_remove: Will be withdrawn}
  mac-only:
    paths: {darwin: /skills}
    applies_to: [Cursor]
    action_labels: {cancel: Stop}
`))
	if err != nil {
		t.Fatal(err)
	}
	variant := map[string]catalog.Variant{Windows: {}}
	item := catalog.Item{
		CustomActionLabels: map[string]string{"install": "Enable for me", "removing": "  "},
		Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{
			"market": variant, "skills": variant, "mac-only": {Darwin: {}},
		}},
	}
	labels, appliesTo := table.Storefront(item, Windows)
	want := map[string]string{
		"install": "Enable for me", "installing": "Adding", "installed": "Added", "remove": "Withdraw",
		"pending_remove": "Will be withdrawn", "applies_to": "Works in",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
	if want := []string{"Claude Code", "Codex"}; !reflect.DeepEqual(appliesTo, want) {
		t.Errorf("appliesTo = %v, want %v", appliesTo, want)
	}

	// A non-zip item has no placements: only its own labels apply.
	plain := catalog.Item{CustomActionLabels: map[string]string{"cancel": "Stop"}, Installer: catalog.InstallerItem{Type: "msi"}}
	labels, appliesTo = table.Storefront(plain, Windows)
	if !reflect.DeepEqual(labels, map[string]string{"cancel": "Stop"}) || appliesTo != nil {
		t.Errorf("non-zip item: labels %v, appliesTo %v", labels, appliesTo)
	}
	if labels, appliesTo = table.Storefront(catalog.Item{}, Windows); labels != nil || appliesTo != nil {
		t.Errorf("unlabelled item: labels %v, appliesTo %v, want both nil", labels, appliesTo)
	}
}

// claudeTemplates returns the Claude catalog and drop-in templates of the
// Tapas fixture's marketplace, which the zip-items guide shows for brands
// to copy.
func claudeTemplates(t *testing.T) (catalog, dropIn string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "test", "manual", "fixtures", "tapas", "placements.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	files := table.Placements["tapas-marketplace"].Files
	if len(files) != 3 || files[0].Path != ".claude-plugin/marketplace.json" || files[2].Paths == nil {
		t.Fatalf("tapas-marketplace files = %+v", files)
	}
	return files[0].Template, files[2].Template
}

// The Claude templates render what the Go encoders before them wrote, as
// canonical JSON: the optional catalog keys only when vars sets them (an
// empty map or list counts as set), "plugins": [] with nothing placed, and
// a drop-in that enables only the managed plugins and renders nothing, so
// the file is removed, when the marketplace holds no plugin.
func TestClaudeTemplates(t *testing.T) {
	catalogTmpl, dropInTmpl := claudeTemplates(t)
	full := Spec{Vars: map[string]any{
		"name":                                "acme",
		"owner":                               map[string]any{"name": "Acme IT"},
		"renames":                             map[string]any{"old-plugin": "new-plugin"},
		"allowCrossMarketplaceDependenciesOn": []any{"other-marketplace"},
	}}
	minimal := Spec{Vars: map[string]any{"name": "acme", "owner": "Acme IT"}}
	empties := Spec{Vars: map[string]any{"name": "acme", "owner": "Acme IT", "renames": map[string]any{}, "allowCrossMarketplaceDependenciesOn": []any{}}}
	items := []render.Item{
		{Name: "alpha", Version: "1.0.0", Path: `C:\m\plugins\alpha`},
		{Name: "zeta", Version: "2.0.0", Managed: true, Path: `C:\m\plugins\zeta`},
	}
	tests := map[string]struct {
		template string
		ctx      render.FileContext
		want     string
	}{
		"catalog": {catalogTmpl, render.FileContext{OS: Windows, Scope: ScopeMachine, Placement: full.View("market", `C:\m`), Items: items}, `{
  "allowCrossMarketplaceDependenciesOn": [
    "other-marketplace"
  ],
  "name": "acme",
  "owner": {
    "name": "Acme IT"
  },
  "plugins": [
    {
      "defaultEnabled": false,
      "name": "alpha",
      "source": "./plugins/alpha",
      "version": "1.0.0"
    },
    {
      "defaultEnabled": true,
      "name": "zeta",
      "source": "./plugins/zeta",
      "version": "2.0.0"
    }
  ],
  "renames": {
    "old-plugin": "new-plugin"
  }
}
`},
		"catalog without the optional keys or plugins": {catalogTmpl, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: minimal.View("market", "/m")}, `{
  "name": "acme",
  "owner": "Acme IT",
  "plugins": []
}
`},
		"catalog with the optional keys empty": {catalogTmpl, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: empties.View("market", "/m")}, `{
  "allowCrossMarketplaceDependenciesOn": [],
  "name": "acme",
  "owner": "Acme IT",
  "plugins": [],
  "renames": {}
}
`},
		"drop-in": {dropInTmpl, render.FileContext{OS: Windows, Scope: ScopeMachine, Placement: full.View("market", `C:\ProgramData\Acme\marketplace`), Items: items}, `{
  "enabledPlugins": {
    "zeta@acme": true
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "path": "C:\\ProgramData\\Acme\\marketplace",
        "source": "directory"
      }
    }
  }
}
`},
		"drop-in with no managed plugin": {dropInTmpl, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: minimal.View("market", "/Library/Application Support/Acme/marketplace"), Items: items[:1]}, `{
  "enabledPlugins": {},
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "path": "/Library/Application Support/Acme/marketplace",
        "source": "directory"
      }
    }
  }
}
`},
		"drop-in with no plugin": {dropInTmpl, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: minimal.View("market", "/m")}, ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpl, err := render.Parse(name, tc.template)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := tmpl.Render(tc.ctx, nil, "x.json")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("rendered\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

// The guide shows the Claude catalog and drop-in as a placement for brands
// to copy, so it must parse and carry the fixture's templates, which the
// golden test above pins.
func TestGuideShowsClaudeTemplates(t *testing.T) {
	const guide = "../../site/src/content/docs/guides/zip-items.md"
	data, err := os.ReadFile(guide)
	if os.IsNotExist(err) {
		t.Skipf("%s is not in this checkout", guide)
	}
	if err != nil {
		t.Fatal(err)
	}
	var block string
	for _, part := range strings.Split(string(data), "```yaml\n")[1:] {
		if body, _, _ := strings.Cut(part, "```"); strings.Contains(body, "extraKnownMarketplaces") {
			block = body
		}
	}
	table, err := Parse([]byte(block))
	if err != nil {
		t.Fatalf("%s: the Claude marketplace example: %v", guide, err)
	}
	catalog, dropIn := claudeTemplates(t)
	files := table.Placements["acme-marketplace"].Files
	if len(files) != 2 || files[0].Template != catalog || files[1].Template != dropIn {
		t.Errorf("%s: the Claude marketplace example's templates differ from the Tapas fixture's", guide)
	}
}
