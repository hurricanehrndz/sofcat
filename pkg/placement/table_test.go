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

// The sample table under examples/ is documentation; it must stay valid.
func TestExampleTableParses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "examples", "placements.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, p := range table.Placements {
		kinds[p.Kind] = true
	}
	if len(kinds) != 3 {
		t.Errorf("example shows kinds %v, want all three", kinds)
	}
}

func TestParse(t *testing.T) {
	table, err := Parse([]byte(`
placements:
  codex-skills:
    kind: directory
    paths:
      darwin: /etc/codex/skills
      windows: ${USERPROFILE}\.agents\skills
    scope: {windows: user}
  market:
    kind: marketplace
    paths: {darwin: /m}
    marketplace: {name: acme, owner: {name: Acme IT}}
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
	if got := table.Placements["market"].Marketplace.Files(); !reflect.DeepEqual(got, DefaultManifestFiles) {
		t.Errorf("manifest files %v", got)
	}
	if got := table.Names(); !reflect.DeepEqual(got, []string{"codex-skills", "market"}) {
		t.Errorf("names %v", got)
	}
	if empty, err := Parse(nil); err != nil || len(empty.Placements) != 0 {
		t.Errorf("empty file: %v, %v", empty, err)
	}
}

// Action labels and "Works in" follow table order, which is the
// order the author wrote, not the alphabet.
func TestNamesKeepFileOrder(t *testing.T) {
	table, err := Parse([]byte(`
placements:
  zed: {kind: directory, paths: {darwin: /z}}
  alpha: {kind: directory, paths: {darwin: /a}}
  mid: {kind: directory, paths: {darwin: /m}}
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
  p: {kind: directory, paths: {darwin: /a}}
  p: {kind: directory, paths: {darwin: /b}}
`))
	if err == nil || !strings.Contains(err.Error(), `"p"`) {
		t.Errorf("duplicate placement: error %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	tests := map[string]struct{ yaml, want string }{
		"unknown field": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, colour: red}`, "colour"},
		"unknown top-level field": {"placement: {}\n", "placement"},
		"bad kind": {`
placements:
  p: {kind: folder, paths: {darwin: /p}}`, "unknown kind"},
		"no paths": {`
placements:
  p: {kind: directory}`, "no paths"},
		"unknown path OS": {`
placements:
  p: {kind: directory, paths: {linux: /p}}`, "unknown OS"},
		"darwin user scope": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, scope: {darwin: user}}`, "darwin: user"},
		"bad scope value": {`
placements:
  p: {kind: directory, paths: {windows: 'C:\p'}, scope: {windows: everyone}}`, "want machine or user"},
		"USERPROFILE in machine scope": {`
placements:
  p: {kind: directory, paths: {windows: '${USERPROFILE}\skills'}}`, "${USERPROFILE}"},
		"unknown action label": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, action_labels: {buy: Buy}}`, "action_labels"},
		"marketplace without block": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}}`, "needs a marketplace block"},
		"marketplace without owner": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: acme}}`, "name and owner"},
		"marketplace manifest outside the root": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [../x.json]}}`, "manifest_files"},
		"user-scope marketplace": {`
placements:
  m: {kind: marketplace, paths: {windows: '${USERPROFILE}\m'}, scope: {windows: user}, marketplace: {name: a, owner: b}}`, "kind directory only"},
		"marketplace block on directory": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, marketplace: {name: a, owner: b}}`, "marketplace block on kind"},
		"settings registering a missing marketplace": {`
placements:
  s: {kind: settings, paths: {darwin: /s.json}, registers: nowhere}`, "registers"},
		"settings registering a directory": {`
placements:
  p: {kind: directory, paths: {darwin: /p}}
  s: {kind: settings, paths: {darwin: /s.json}, registers: p}`, "not a marketplace"},
		"registers on directory": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, registers: p}`, "registers on kind"},
		"name with a path": {`
placements:
  a/b: {kind: directory, paths: {darwin: /p}}`, "invalid name"},
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

// Labels resolve item first, then the first placement in table order (not
// variant or map order) that sets the key; works_in is the ordered union.
// A placement without a variant on this OS contributes nothing.
func TestStorefront(t *testing.T) {
	table, err := Parse([]byte(`
placements:
  skills:
    kind: directory
    paths: {windows: 'C:\skills'}
    works_in: [Claude Code]
    action_labels: {install: Add, installing: Adding, installed: Added}
  market:
    kind: marketplace
    paths: {windows: 'C:\market'}
    works_in: [Claude Code, Codex]
    action_labels: {install: Make available, installing: Making available, remove: Withdraw, pending_remove: Will be withdrawn}
    marketplace: {name: m, owner: {name: o}}
  mac-only:
    kind: directory
    paths: {darwin: /skills}
    works_in: [Cursor]
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
	labels, worksIn := table.Storefront(item, Windows)
	want := map[string]string{
		"install": "Enable for me", "installing": "Adding", "installed": "Added", "remove": "Withdraw",
		"pending_remove": "Will be withdrawn",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
	if want := []string{"Claude Code", "Codex"}; !reflect.DeepEqual(worksIn, want) {
		t.Errorf("worksIn = %v, want %v", worksIn, want)
	}

	// A non-zip item has no placements: only its own labels apply.
	plain := catalog.Item{CustomActionLabels: map[string]string{"cancel": "Stop"}, Installer: catalog.InstallerItem{Type: "msi"}}
	labels, worksIn = table.Storefront(plain, Windows)
	if !reflect.DeepEqual(labels, map[string]string{"cancel": "Stop"}) || worksIn != nil {
		t.Errorf("non-zip item: labels %v, worksIn %v", labels, worksIn)
	}
	if labels, worksIn = table.Storefront(catalog.Item{}, Windows); labels != nil || worksIn != nil {
		t.Errorf("unlabelled item: labels %v, worksIn %v, want both nil", labels, worksIn)
	}
}

// A manifest_files entry is a path, which gets the default catalog
// template, or {path, template}; a settings placement may carry a template
// and then register a directory; check_facts is a free map.
func TestParseTemplates(t *testing.T) {
	table, err := Parse([]byte(`
check_facts: {site: east, floors: [1, 2]}
placements:
  skills: {kind: directory, paths: {darwin: /s}}
  market:
    kind: marketplace
    paths: {darwin: /m}
    marketplace:
      name: acme
      owner: {name: Acme}
      manifest_files:
        - .claude-plugin/marketplace.json
        - path: .agents/plugins/marketplace.json
          template: |
            {"name": {{ json .Placement.Marketplace.Name }}}
  index:
    kind: settings
    paths: {darwin: /index.json}
    registers: skills
    template: '{{ if .Items }}{{ json .Items }}{{ end }}'
  drop-in: {kind: settings, paths: {darwin: /d.json}, registers: market}
`))
	if err != nil {
		t.Fatal(err)
	}
	m := table.Placements["market"].Marketplace
	want := []ManifestFile{
		{Path: ".claude-plugin/marketplace.json", Template: DefaultCatalogTemplate},
		{Path: ".agents/plugins/marketplace.json", Template: "{\"name\": {{ json .Placement.Marketplace.Name }}}\n"},
	}
	if !reflect.DeepEqual(m.Manifests(), want) {
		t.Errorf("manifests = %+v", m.Manifests())
	}
	if m.ManifestFiles[0].Template != "" {
		t.Errorf("Manifests filled the parsed entry in: %+v", m.ManifestFiles[0])
	}
	if got := m.Files(); !reflect.DeepEqual(got, []string{".claude-plugin/marketplace.json", ".agents/plugins/marketplace.json"}) {
		t.Errorf("files = %v", got)
	}
	if got := table.Placements["index"].SettingsTemplate(); got != "{{ if .Items }}{{ json .Items }}{{ end }}" {
		t.Errorf("settings template = %q", got)
	}
	if got := table.Placements["drop-in"].SettingsTemplate(); got != DefaultSettingsTemplate {
		t.Errorf("default settings template = %q", got)
	}
	if reg := table.Placements["market"].Registered("market", "/m"); reg.Kind != KindMarketplace || reg.Root != "/m" || reg.Marketplace == nil || reg.Marketplace.Name != "acme" {
		t.Errorf("registered = %+v", reg)
	}
	if reg := table.Placements["skills"].Registered("skills", "/s"); reg.Marketplace != nil {
		t.Errorf("registered directory has a marketplace: %+v", reg)
	}
	if table.CheckFacts["site"] != "east" || len(table.CheckFacts) != 2 {
		t.Errorf("check_facts = %v", table.CheckFacts)
	}
	view := table.Placements["market"].View("market", "/m")
	if view.Kind != KindMarketplace || view.Marketplace == nil || view.Marketplace.Name != "acme" || view.Marketplace.Owner.(map[string]any)["name"] != "Acme" {
		t.Errorf("view = %+v", view)
	}
	if view := table.Placements["skills"].View("skills", "/s"); view.Marketplace != nil {
		t.Errorf("directory view has a marketplace: %+v", view)
	}
}

// Templates are checked at load, so a repository fails -check rather than
// every device failing the placement.
func TestParseRejectsTemplates(t *testing.T) {
	tests := map[string]struct{ yaml, want string }{
		"manifest entry with an unknown field": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [{path: x.json, mode: 644}]}}`, "unknown field"},
		"manifest entry that is a list": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [[x.json]]}}`, "path or {path, template}"},
		"manifest template that does not parse": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [{path: x.json, template: '{{ .Items'}]}}`, `manifest_files "x.json"`},
		"manifest template calling now()": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [{path: x.json, template: '{{ cond "now() > date(\"2020-01-01\")" }}'}]}}`, "now()"},
		"settings template that does not parse": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b}}
  s: {kind: settings, paths: {darwin: /s.json}, registers: m, template: '{{ end }}'}`, "unexpected {{end}}"},
		"settings registering a settings placement": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b}}
  s: {kind: settings, paths: {darwin: /s.json}, registers: m}
  t: {kind: settings, paths: {darwin: /t.json}, registers: s, template: x}`, `registers "s", which is a settings placement`},
		"settings template registering a user-scope placement": {`
placements:
  p: {kind: directory, paths: {windows: '${USERPROFILE}\p'}, scope: {windows: user}}
  s: {kind: settings, paths: {windows: 'C:\s.json'}, registers: p, template: x}`, `placement "s": registers "p", which has user scope`},
		"manifest path listed twice": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [.claude-plugin/marketplace.json, {path: .claude-plugin/marketplace.json, template: x}]}}`, `manifest_files: ".claude-plugin/marketplace.json" is listed twice`},
		"template on a directory": {`
placements:
  p: {kind: directory, paths: {darwin: /p}, template: x}`, "template on kind"},
		"template on a marketplace": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b}, template: x}`, "template on kind"},
		"manifest path without a template that is not .json": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [catalog]}}`, `manifest_files "catalog" without a template must end in .json: the default template is the Claude catalog`},
		"settings path without a template that is not .json": {`
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b}}
  s: {kind: settings, paths: {darwin: /s.json, windows: 'C:\s.txt'}, registers: m}`, `placement "s": paths: windows path "C:\\s.txt" without a template must end in .json: the default template is the Claude Code drop-in`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}

// Only the defaults need a .json path: a template of the brand's own is
// written as rendered, to whatever path it names.
func TestParseAcceptsTemplatedNonJSONPaths(t *testing.T) {
	tests := map[string]string{
		"manifest entry": `
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b, manifest_files: [{path: catalog.toml, template: x}]}}`,
		"settings placement": `
placements:
  m: {kind: marketplace, paths: {darwin: /m}, marketplace: {name: a, owner: b}}
  s: {kind: settings, paths: {darwin: /s.toml, windows: 'C:\s.toml'}, registers: m, template: x}`,
	}
	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(yaml)); err != nil {
				t.Error(err)
			}
		})
	}
}

// The guide shows each default template verbatim as the text a brand copies
// to override it, so an edit to a constant must reach the guide too. Each
// constant ends in a newline and the guide's fenced block closes on the line
// after it, so the match includes that newline.
func TestGuideShowsDefaultTemplates(t *testing.T) {
	const guide = "../../site/src/content/docs/guides/zip-items.md"
	data, err := os.ReadFile(guide)
	if os.IsNotExist(err) {
		t.Skipf("%s is not in this checkout", guide)
	}
	if err != nil {
		t.Fatal(err)
	}
	for name, template := range map[string]string{
		"DefaultCatalogTemplate":  DefaultCatalogTemplate,
		"DefaultSettingsTemplate": DefaultSettingsTemplate,
	} {
		if !strings.Contains(string(data), "```\n"+template+"```") {
			t.Errorf("%s does not show %s verbatim in a fenced block", guide, name)
		}
	}
}

// The default templates render what the Go encoders they replaced wrote,
// as canonical JSON: the optional marketplace keys only when the block sets
// them (an empty map or list counts as set), "plugins": [] with nothing
// placed, and a drop-in that enables only the managed plugins and renders
// nothing, so the file is removed, when the marketplace holds no plugin. A
// change to the default text shows up here.
func TestDefaultTemplates(t *testing.T) {
	full := Spec{Kind: KindMarketplace, Marketplace: &Marketplace{
		Name:                                "acme",
		Owner:                               map[string]any{"name": "Acme IT"},
		Renames:                             map[string]any{"old-plugin": "new-plugin"},
		AllowCrossMarketplaceDependenciesOn: []any{"other-marketplace"},
	}}
	minimal := Spec{Kind: KindMarketplace, Marketplace: &Marketplace{Name: "acme", Owner: "Acme IT"}}
	empties := Spec{Kind: KindMarketplace, Marketplace: &Marketplace{Name: "acme", Owner: "Acme IT", Renames: map[string]any{}, AllowCrossMarketplaceDependenciesOn: []any{}}}
	items := []render.Item{
		{Name: "alpha", Version: "1.0.0", Path: `C:\m\plugins\alpha`},
		{Name: "zeta", Version: "2.0.0", Managed: true, Path: `C:\m\plugins\zeta`},
	}
	tests := map[string]struct {
		template string
		ctx      render.FileContext
		want     string
	}{
		"catalog": {DefaultCatalogTemplate, render.FileContext{OS: Windows, Scope: ScopeMachine, Placement: full.View("market", `C:\m`), Items: items}, `{
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
		"catalog without the optional keys or plugins": {DefaultCatalogTemplate, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: minimal.View("market", "/m")}, `{
  "name": "acme",
  "owner": "Acme IT",
  "plugins": []
}
`},
		"catalog with the optional keys empty": {DefaultCatalogTemplate, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: empties.View("market", "/m")}, `{
  "allowCrossMarketplaceDependenciesOn": [],
  "name": "acme",
  "owner": "Acme IT",
  "plugins": [],
  "renames": {}
}
`},
		"drop-in": {DefaultSettingsTemplate, render.FileContext{OS: Windows, Scope: ScopeMachine, Placement: Spec{Kind: KindSettings}.View("drop-in", `C:\s.json`), Registers: full.Registered("market", `C:\ProgramData\Acme\marketplace`), Items: items}, `{
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
		"drop-in with no managed plugin": {DefaultSettingsTemplate, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: Spec{Kind: KindSettings}.View("drop-in", "/s.json"), Registers: minimal.Registered("market", "/Library/Application Support/Acme/marketplace"), Items: items[:1]}, `{
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
		"drop-in with no plugin": {DefaultSettingsTemplate, render.FileContext{OS: Darwin, Scope: ScopeMachine, Placement: Spec{Kind: KindSettings}.View("drop-in", "/s.json"), Registers: minimal.Registered("market", "/m")}, ""},
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
