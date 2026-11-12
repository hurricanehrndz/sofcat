package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// marketTest is an installer with one write root holding placement
// "market": a Claude Code marketplace with plugins under plugins/, the Claude
// catalog at two relative paths and the drop-in at its own path, all from
// the Tapas fixture's templates, parsed as a repository would serve them.
type marketTest struct {
	z        *ZipInstaller
	root     string // the marketplace root
	settings string // the drop-in's path
}

// claudeTemplates returns the Tapas fixture's Claude catalog and drop-in
// templates, which the zip-items guide shows for brands to copy.
func claudeTemplates(t *testing.T) (catalog, dropIn string) {
	t.Helper()
	dir := filepath.Join("..", "..", "test", "manual", "fixtures", "tapas")
	body, err := os.ReadFile(filepath.Join(dir, "placements.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := placement.Parse(body, func(rel string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	})
	if err != nil {
		t.Fatal(err)
	}
	files := table.Placements["tapas-marketplace"].Files
	return files[0].Template, files[2].Template
}

func newMarketTest(t *testing.T, settings string) marketTest {
	t.Helper()
	base := tempDir(t)
	roots := filepath.Join(base, "roots")
	root := filepath.Join(roots, "market")
	if settings == "" {
		settings = filepath.Join(roots, "settings.d", "50-acme.json")
	}
	table, err := placement.Parse(fmt.Appendf(nil, `placements:
  market:
    paths: {%[1]s: '%[2]s'}
    exclusive: true
    items_dir: plugins
    vars:
      name: acme
      owner: {name: Acme IT, email: it@acme.test}
      renames:
        old-plugin: new-plugin
        older: {to: new-plugin, since: 2, keep: true, aliases: [a, b]}
      allowCrossMarketplaceDependenciesOn: [other, {name: third, only: [x]}]
    files:
      - {path: .claude-plugin/marketplace.json, template: x}
      - {path: .agents/plugins/marketplace.json, template: x}
      - {paths: {%[1]s: '%[3]s'}, template: x}
`, testOS(), root, settings), nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, dropIn := claudeTemplates(t)
	files := table.Placements["market"].Files
	files[0].Template, files[1].Template, files[2].Template = catalog, catalog, dropIn
	return marketTest{
		z: &ZipInstaller{
			Guard: placement.Guard{
				GOOS:    testOS(),
				Roots:   placement.Roots{Darwin: []string{roots}, Windows: []string{roots}},
				DataDir: filepath.Join(base, "data"),
			},
			Table:      table,
			ReceiptDir: filepath.Join(base, "data", "receipts"),
			Slug:       "sofcat-test",
		},
		root:     root,
		settings: settings,
	}
}

// plugin packs a plugin tree as item name for placement.
func plugin(t *testing.T, name, placementName string) (catalog.Item, string) {
	t.Helper()
	archive := filepath.Join(t.TempDir(), name+".zip")
	files := map[string]string{".claude-plugin/plugin.json": `{"name": "` + name + `"}`, "README.md": name}
	packed, err := artifact.PackFile(writeTree(t, files), testOS(), archive)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.Item{
		Name:    name,
		Version: "1.0.0",
		Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{
			placementName: {testOS(): {Location: name + ".zip", Hash: packed.Hash, TreeHash: packed.TreeHash}},
		}},
	}, archive
}

// install places a plugin in the marketplace with the given managed flag.
func (m marketTest) install(t *testing.T, name string, managed bool) {
	t.Helper()
	item, archive := plugin(t, name, "market")
	m.z.Managed = func(string) bool { return managed }
	wantOutcome(t, "install "+name, m.z.Apply(item, target(t, m.z, item, "market"), archive), OutcomeInstalled)
}

func (m marketTest) manifest(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.root, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// enabled returns the drop-in's enabledPlugins, failing when the file is not
// valid JSON.
func (m marketTest) enabled(t *testing.T) map[string]bool {
	t.Helper()
	var d struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	data, err := os.ReadFile(m.settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return d.EnabledPlugins
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestMarketplaceGolden: the generated files are what Claude Code and Codex
// read, so their bytes are fixed: owner, renames and
// allowCrossMarketplaceDependenciesOn pass through as configured, every
// manifest file gets the same content, entries carry the receipt's version
// and managed flag, and enabledPlugins lists only managed plugins, keyed
// <plugin>@<marketplace>.
func TestMarketplaceGolden(t *testing.T) {
	m := newMarketTest(t, "")
	m.install(t, "zeta", true)
	m.install(t, "alpha", false)

	if !exists(filepath.Join(m.root, "plugins", "zeta", "README.md")) || !exists(filepath.Join(m.root, ".sofcat-test-owned")) {
		t.Fatal("plugin not placed at <root>/plugins/<item>, or the root has no marker")
	}
	// Canonical JSON: keys sorted at every level.
	wantManifest := `{
  "allowCrossMarketplaceDependenciesOn": [
    "other",
    {
      "name": "third",
      "only": [
        "x"
      ]
    }
  ],
  "name": "acme",
  "owner": {
    "email": "it@acme.test",
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
      "version": "1.0.0"
    }
  ],
  "renames": {
    "old-plugin": "new-plugin",
    "older": {
      "aliases": [
        "a",
        "b"
      ],
      "keep": true,
      "since": 2,
      "to": "new-plugin"
    }
  }
}
`
	if got := m.manifest(t); got != wantManifest {
		t.Errorf("marketplace.json =\n%s\nwant\n%s", got, wantManifest)
	}
	if got, _ := os.ReadFile(filepath.Join(m.root, ".agents", "plugins", "marketplace.json")); string(got) != wantManifest {
		t.Errorf("second manifest file differs:\n%s", got)
	}

	wantSettings := `{
  "enabledPlugins": {
    "zeta@acme": true
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "path": ` + jsonString(t, m.root) + `,
        "source": "directory"
      }
    }
  }
}
`
	if got, _ := os.ReadFile(m.settings); string(got) != wantSettings {
		t.Errorf("drop-in =\n%s\nwant\n%s", got, wantSettings)
	}
	// Written through a temp file renamed into place: nothing else is left.
	if entries, _ := os.ReadDir(filepath.Dir(m.settings)); len(entries) != 1 {
		t.Errorf("settings directory holds %v, want only the drop-in", entries)
	}
}

// TestMarketplaceFlagOnlyChange: moving a plugin between optional and
// managed changes what harnesses enable, so the generated files follow the
// receipt's flag, while the plugin's tree is left as it is.
func TestMarketplaceFlagOnlyChange(t *testing.T) {
	m := newMarketTest(t, "")
	m.install(t, "beta", false)
	if strings.Contains(m.manifest(t), `"defaultEnabled": true`) || len(m.enabled(t)) != 0 {
		t.Fatalf("optional plugin enabled:\n%s", m.manifest(t))
	}
	sentinel := filepath.Join(m.root, "plugins", "beta", "README.md")
	before, err := os.Stat(sentinel)
	if err != nil {
		t.Fatal(err)
	}

	item, _ := plugin(t, "beta", "market")
	m.z.Managed = func(string) bool { return true }
	wantOutcome(t, "now managed", m.z.Apply(item, target(t, m.z, item, "market"), ""), OutcomeAdopted)
	if !strings.Contains(m.manifest(t), `"defaultEnabled": true`) || !m.enabled(t)["beta@acme"] {
		t.Errorf("flag change not reflected: %s, %v", m.manifest(t), m.enabled(t))
	}
	after, err := os.Stat(sentinel)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the flag change touched the tree: %v", err)
	}
}

// TestMarketplaceHeldPluginKeepsFlag: a run cannot tell whether a plugin held
// by an unknown condition is managed, so its receipt keeps the flag, and the
// plugin keeps defaultEnabled and its enabledPlugins entry, even when the run
// applies it (another list desires it) with Managed reporting false.
func TestMarketplaceHeldPluginKeepsFlag(t *testing.T) {
	m := newMarketTest(t, "")
	m.install(t, "held", true)
	m.install(t, "other", false) // Managed now reports false for every item
	m.z.Held = func(item string) bool { return item == "held" }

	before, _, err := receipt.Read(m.z.ReceiptDir, "market", "held")
	if err != nil {
		t.Fatal(err)
	}
	manifest, settings := m.manifest(t), readFile(t, m.settings)
	item, _ := plugin(t, "held", "market")
	wantOutcome(t, "apply held", m.z.Apply(item, target(t, m.z, item, "market"), ""), OutcomeCurrent)
	if after, _, _ := receipt.Read(m.z.ReceiptDir, "market", "held"); !reflect.DeepEqual(after, before) {
		t.Errorf("held receipt rewritten: %+v, was %+v", after, before)
	}
	if m.manifest(t) != manifest || readFile(t, m.settings) != settings {
		t.Errorf("generated files changed:\n%s\n%s", m.manifest(t), readFile(t, m.settings))
	}
	if !strings.Contains(manifest, `"defaultEnabled": true,
      "name": "held",
      "source": "./plugins/held",
      "version": "1.0.0"`) {
		t.Errorf("held plugin's entry changed:\n%s", manifest)
	}
	if got := m.enabled(t); !got["held@acme"] || len(got) != 1 {
		t.Errorf("enabledPlugins = %v, want only held@acme", got)
	}

	// A receipt rewritten for another reason (a new version of the same
	// tree) keeps the held flag too.
	item.Version = "1.0.1"
	wantOutcome(t, "new version", m.z.Apply(item, target(t, m.z, item, "market"), ""), OutcomeAdopted)
	if rc, _, _ := receipt.Read(m.z.ReceiptDir, "market", "held"); rc.Version != "1.0.1" || !rc.Managed {
		t.Errorf("held receipt after new version = %+v, want 1.0.1 managed", rc)
	}
	if !m.enabled(t)["held@acme"] {
		t.Error("held plugin disabled by the new version")
	}
}

// TestMarketplaceRegenerateRefusalHeals: a drop-in path refused on install
// leaves a receipt, so the next Apply reports current; the per-run pass
// reports the refusal against the marketplace placement on every run until
// the path is allowed, and then writes the drop-in.
func TestMarketplaceRegenerateRefusalHeals(t *testing.T) {
	outsideRoot := tempDir(t)
	outside := filepath.Join(outsideRoot, "settings.d", "50-acme.json")
	m := newMarketTest(t, outside)
	item, archive := plugin(t, "one", "market")
	m.z.Managed = func(string) bool { return true }
	wantOutcome(t, "install", m.z.Apply(item, target(t, m.z, item, "market"), archive), OutcomeRefused)
	wantOutcome(t, "next apply", m.z.Apply(item, target(t, m.z, item, "market"), ""), OutcomeCurrent)

	r := &Runner{Report: report.New(), Zip: m.z}
	r.Regenerate()
	if len(r.Report.FailedItems) != 1 {
		t.Fatalf("failed items = %+v, want one", r.Report.FailedItems)
	}
	if f := r.Report.FailedItems[0]; f.Name != "market" || f.Action != "generate" || f.Code != report.CodePlacementRefused || !strings.Contains(f.Error, filepath.Dir(outside)) {
		t.Errorf("failed item = %+v, want market generate placement_refused naming the drop-in path", f)
	}
	if exists(filepath.Dir(outside)) {
		t.Error("wrote outside the write roots")
	}

	m.z.Guard.Roots.Darwin = append(m.z.Guard.Roots.Darwin, outsideRoot)
	m.z.Guard.Roots.Windows = append(m.z.Guard.Roots.Windows, outsideRoot)
	if failed := m.z.RegenerateAll(); len(failed) != 0 {
		t.Fatalf("RegenerateAll = %+v", failed)
	}
	if !m.enabled(t)["one@acme"] {
		t.Error("drop-in not written once the refusal was lifted")
	}
}

// TestMarketplaceRegenerateRestoresDeleted: a hand-deleted manifest is not a
// tree change, so only the per-run pass restores it. A marketplace never
// placed into is left alone.
func TestMarketplaceRegenerateRestoresDeleted(t *testing.T) {
	m := newMarketTest(t, "")
	if failed := m.z.RegenerateAll(); len(failed) != 0 || exists(m.root) || exists(m.settings) {
		t.Fatalf("empty marketplace: RegenerateAll = %+v, wrote files: %v", failed, exists(m.root))
	}
	m.install(t, "one", true)
	want := m.manifest(t)
	path := filepath.Join(m.root, ".claude-plugin", "marketplace.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if failed := m.z.RegenerateAll(); len(failed) != 0 {
		t.Fatalf("RegenerateAll = %+v", failed)
	}
	if got := m.manifest(t); got != want {
		t.Errorf("restored manifest =\n%s\nwant\n%s", got, want)
	}
}

// TestRegenerateAllSkipsUserScope: the service has no user to expand a
// user-scope root for, so its per-run pass leaves those placements to the
// per-user helper instead of reporting a failed generate on every run.
// Regenerate, which the helper calls, still takes them.
func TestRegenerateAllSkipsUserScope(t *testing.T) {
	z, _ := newTestZip(t)
	z.Table.Placements["user-plugins"] = placement.Spec{
		Paths: map[string]string{testOS(): "${USERPROFILE}/plugins"},
		Scope: map[string]string{testOS(): placement.ScopeUser},
		Files: []placement.File{{Path: "index.json", Template: "{}"}},
	}
	z.Table.Order = append(z.Table.Order, "user-plugins")
	if failed := z.RegenerateAll(); len(failed) != 0 {
		t.Fatalf("RegenerateAll = %+v, want the user-scope placement skipped", failed)
	}
	if failed, _ := z.Regenerate([]string{"user-plugins"}); len(failed) != 1 {
		t.Errorf("Regenerate(user-plugins) without USERPROFILE = %+v, want one failure", failed)
	}
}

// TestMarketplaceLastPluginRemoved: with no plugin left the drop-in goes, so
// the harness stops loading an empty marketplace, and the manifest stays
// valid with no plugins.
func TestMarketplaceLastPluginRemoved(t *testing.T) {
	m := newMarketTest(t, "")
	m.install(t, "one", true)
	m.install(t, "two", false)
	rcs, err := receipt.List(m.z.ReceiptDir)
	if err != nil || len(rcs) != 2 {
		t.Fatalf("receipts = %v, %v", rcs, err)
	}
	wantOutcome(t, "remove one", m.z.Remove(rcs[0]), OutcomeRemoved)
	if strings.Contains(m.manifest(t), `"one"`) || len(m.enabled(t)) != 0 || !exists(m.settings) {
		t.Fatalf("after removing one: %s", m.manifest(t))
	}
	wantOutcome(t, "remove two", m.z.Remove(rcs[1]), OutcomeRemoved)
	if exists(m.settings) {
		t.Error("drop-in kept with no plugin left")
	}
	var manifest struct {
		Name    string `json:"name"`
		Plugins []any  `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(m.manifest(t)), &manifest); err != nil || manifest.Name != "acme" || manifest.Plugins == nil || len(manifest.Plugins) != 0 {
		t.Errorf("empty manifest = %s (%v)", m.manifest(t), err)
	}
}

// TestPlacementFileEmptyRender: an empty render removes a file at a
// relative path as at its own path, and an entry with no path on this OS is
// skipped.
func TestPlacementFileEmptyRender(t *testing.T) {
	m := newMarketTest(t, "")
	other := placement.Darwin
	if testOS() == placement.Darwin {
		other = placement.Windows
	}
	spec := m.z.Table.Placements["market"]
	spec.Files = append(spec.Files,
		placement.File{Path: ".index.json", Template: `{{ if .Items }}{"n": {{ len .Items }}}{{ end }}`},
		placement.File{Paths: map[string]string{other: "/elsewhere.json"}, Template: "{}"},
	)
	m.z.Table.Placements["market"] = spec
	m.install(t, "one", true)
	index := filepath.Join(m.root, ".index.json")
	if got := readFile(t, index); got != "{\n  \"n\": 1\n}\n" {
		t.Errorf(".index.json = %q", got)
	}
	rcs, err := receipt.List(m.z.ReceiptDir)
	if err != nil || len(rcs) != 1 {
		t.Fatalf("receipts = %v, %v", rcs, err)
	}
	wantOutcome(t, "remove", m.z.Remove(rcs[0]), OutcomeRemoved)
	if exists(index) || exists(m.settings) {
		t.Error("empty renders kept their files")
	}
}

// TestFilePathIntoTreeRefused: an absolute files path is checked against the
// placement too, once resolved: one inside an item tree is refused, and so is
// one that two entries name, whatever their case.
func TestFilePathIntoTreeRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		path func(m marketTest) string
		want string
	}{
		"inside an item tree": {func(m marketTest) string { return filepath.Join(m.root, "plugins", "one", "x.json") }, `is under items_dir "plugins"`},
		"the drop-in again": {func(m marketTest) string {
			return filepath.Join(filepath.Dir(m.settings), strings.ToUpper(filepath.Base(m.settings)))
		}, "named by two files entries"},
	} {
		t.Run(name, func(t *testing.T) {
			m := newMarketTest(t, "")
			spec := m.z.Table.Placements["market"]
			spec.Files = append(spec.Files, placement.File{Paths: map[string]string{testOS(): tc.path(m)}, Template: "{}"})
			m.z.Table.Placements["market"] = spec
			item, archive := plugin(t, "one", "market")
			res := m.z.Apply(item, target(t, m.z, item, "market"), archive)
			if res.Outcome != OutcomeRefused || res.Code() != report.CodePlacementRefused || !strings.Contains(res.Err.Error(), tc.want) {
				t.Errorf("result = %s %q (%v), want refused: %s", res.Outcome, res.Code(), res.Err, tc.want)
			}
			if exists(filepath.Join(m.root, "plugins", "one", "x.json")) {
				t.Error("wrote into the item tree")
			}
		})
	}
}

// TestItemsDirLinkRefused: an items_dir that already exists as a link out of
// the write roots is refused before any tree is written through it.
func TestItemsDirLinkRefused(t *testing.T) {
	m := newMarketTest(t, "")
	outside := tempDir(t)
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(m.root, "plugins")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	// Owned, so the exclusive check passes and the link is what is refused.
	if err := os.WriteFile(filepath.Join(m.root, ".sofcat-test-owned"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	item, archive := plugin(t, "one", "market")
	res := m.z.Apply(item, target(t, m.z, item, "market"), archive)
	if res.Outcome != OutcomeRefused || res.Code() != report.CodePlacementRefused {
		t.Errorf("result = %s %q (%v), want refused", res.Outcome, res.Code(), res.Err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote through the link: %v", entries)
	}
}

// TestSettingsPathOutsideRootsRefused: the drop-in's path passes the same
// guard as any placement. The plugin is placed, but the item fails with
// placement_refused naming the placement. The catalogs are still written,
// so one bad file does not hold back the rest, unless files_atomic asks
// for all or none.
func TestSettingsPathOutsideRootsRefused(t *testing.T) {
	for _, atomic := range []bool{false, true} {
		t.Run(fmt.Sprintf("files_atomic=%t", atomic), func(t *testing.T) {
			outside := filepath.Join(tempDir(t), "settings.d", "50-acme.json")
			m := newMarketTest(t, outside)
			spec := m.z.Table.Placements["market"]
			spec.FilesAtomic = atomic
			m.z.Table.Placements["market"] = spec
			item, archive := plugin(t, "one", "market")
			res := m.z.Apply(item, target(t, m.z, item, "market"), archive)
			if res.Outcome != OutcomeRefused || res.Code() != report.CodePlacementRefused || !strings.Contains(res.Err.Error(), `"market"`) {
				t.Errorf("result = %s %q (%v), want refused naming market", res.Outcome, res.Code(), res.Err)
			}
			if exists(filepath.Dir(outside)) {
				t.Error("wrote outside the write roots")
			}
			if !exists(filepath.Join(m.root, "plugins", "one")) {
				t.Error("plugin not placed")
			}
			for _, catalog := range []string{".claude-plugin", filepath.Join(".agents", "plugins")} {
				if got := exists(filepath.Join(m.root, catalog, "marketplace.json")); got == atomic {
					t.Errorf("%s catalog written = %t, want %t", catalog, got, !atomic)
				}
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TestTemplateFileRendersLikeInline: a template read from a template_file
// writes the same bytes as the same text inline, for a .json output and a
// raw one.
func TestTemplateFileRendersLikeInline(t *testing.T) {
	templates := map[string]string{
		"templates/index.json.tmpl": `{{ if .Items }}{"skills": [{{ range $i, $p := .Items }}{{ if $i }},{{ end }}{{ json $p.Name }}{{ end }}]}{{ end }}` + "\n",
		"templates/index.txt.tmpl":  "{{ range .Items }}{{ .Name }} {{ .Version }} {{ .Managed }}\n{{ end }}",
	}
	render := func(jsonEntry, textEntry string) (string, string) {
		m := newMarketTest(t, "")
		table, err := placement.Parse(fmt.Appendf(nil, `placements:
  market:
    paths: {%s: '%s'}
    items_dir: plugins
    files:
      - {path: .index.json, %s}
      - {path: .index.txt, %s}
`, testOS(), m.root, jsonEntry, textEntry), func(rel string) ([]byte, error) {
			return []byte(templates[rel]), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		m.z.Table = table
		m.install(t, "one", true)
		return readFile(t, filepath.Join(m.root, ".index.json")), readFile(t, filepath.Join(m.root, ".index.txt"))
	}
	inlineJSON, inlineText := render(
		"template: "+jsonString(t, templates["templates/index.json.tmpl"]),
		"template: "+jsonString(t, templates["templates/index.txt.tmpl"]))
	fileJSON, fileText := render("template_file: templates/index.json.tmpl", "template_file: templates/index.txt.tmpl")
	if inlineJSON != fileJSON || inlineText != fileText {
		t.Errorf("inline wrote %q and %q, template_file wrote %q and %q", inlineJSON, inlineText, fileJSON, fileText)
	}
	if inlineText != "one 1.0.0 true\n" {
		t.Errorf(".index.txt = %q", inlineText)
	}
}

// addRootless adds rootless placement "harness" with files to z's table,
// last in order.
func addRootless(z *ZipInstaller, files ...placement.File) {
	z.Table.Placements["harness"] = placement.Spec{Files: files}
	z.Table.Order = append(z.Table.Order, "harness")
}

// TestRootlessPlacementRenders: a rootless placement's files are written on
// every run, though no root exists to gate them, with no root and no items;
// a refused one fails under the placement's name.
func TestRootlessPlacementRenders(t *testing.T) {
	z, roots := newTestZip(t)
	settings := filepath.Join(roots, "etc", "harness", "settings.json")
	other := placement.Darwin
	if testOS() == placement.Darwin {
		other = placement.Windows
	}
	addRootless(z,
		placement.File{Paths: map[string]string{testOS(): settings}, Template: `{"root": {{ json .Placement.Root }}, "items": {{ len .Items }}}`},
		placement.File{Paths: map[string]string{other: "/elsewhere.json"}, Template: "{}"},
	)
	if failed := z.RegenerateAll(); len(failed) != 0 {
		t.Fatalf("RegenerateAll = %+v", failed)
	}
	if got := readFile(t, settings); got != "{\n  \"items\": 0,\n  \"root\": \"\"\n}\n" {
		t.Errorf("settings.json = %q", got)
	}

	outside := filepath.Join(tempDir(t), "settings.json")
	z.Table.Placements["harness"] = placement.Spec{Files: []placement.File{{Paths: map[string]string{testOS(): outside}, Template: "{}"}}}
	failed := z.RegenerateAll()
	if len(failed) != 1 || failed[0].Placement != "harness" || failed[0].Code() != report.CodePlacementRefused {
		t.Errorf("RegenerateAll outside the roots = %+v, want one refusal for harness", failed)
	}
	if exists(outside) {
		t.Error("wrote outside the write roots")
	}
}

// TestEnsureAbsent: an absent entry removes its file on every run where it
// exists, at a relative path as at its own, rootless or not, and a missing
// file is no failure.
func TestEnsureAbsent(t *testing.T) {
	z, roots := newTestZip(t)
	old := filepath.Join(roots, "skills", ".old.json")
	retired := filepath.Join(roots, "etc", "retired.json")
	spec := z.Table.Placements["skills"]
	spec.Files = []placement.File{{Path: ".old.json", Ensure: placement.EnsureAbsent}}
	z.Table.Placements["skills"] = spec
	addRootless(z, placement.File{Paths: map[string]string{testOS(): retired}, Ensure: placement.EnsureAbsent})
	for run := range 2 {
		for _, path := range []string{old, retired} {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if run == 0 {
				if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if failed := z.RegenerateAll(); len(failed) != 0 {
			t.Fatalf("run %d: RegenerateAll = %+v", run, failed)
		}
		if exists(old) || exists(retired) {
			t.Errorf("run %d: absent files kept", run)
		}
	}
}

// TestRemoveLeavesLinksAndDirectories: removal, absent or an empty render,
// deletes only a regular file. A symlink stays, with its target, and so
// does a directory, empty or not; each is refused.
func TestRemoveLeavesLinksAndDirectories(t *testing.T) {
	for _, absent := range []bool{true, false} {
		for name, setup := range map[string]func(t *testing.T, path, target string){
			"symlink": func(t *testing.T, path, target string) {
				t.Helper()
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("no symlinks here: %v", err)
				}
			},
			"directory": func(t *testing.T, path, _ string) {
				t.Helper()
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		} {
			t.Run(fmt.Sprintf("%s absent=%t", name, absent), func(t *testing.T) {
				z, roots := newTestZip(t)
				dir := filepath.Join(roots, "etc")
				path, target := filepath.Join(dir, "x.json"), filepath.Join(dir, "target.json")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
				setup(t, path, target)
				f := placement.File{Paths: map[string]string{testOS(): path}, Template: `{{ if false }}{}{{ end }}`}
				if absent {
					f = placement.File{Paths: f.Paths, Ensure: placement.EnsureAbsent}
				}
				addRootless(z, f)
				failed := z.RegenerateAll()
				if len(failed) != 1 || failed[0].Code() != report.CodePlacementRefused || !strings.Contains(failed[0].Err.Error(), "not a regular file") {
					t.Errorf("RegenerateAll = %+v, want one refusal", failed)
				}
				if !exists(path) || !exists(target) {
					t.Errorf("removed: path %t, target %t", !exists(path), !exists(target))
				}
			})
		}
	}
}

// TestUnchangedFileNotRewritten: a file that already holds the render is
// left as it is, the same file on disk, so its watchers see no change. One
// edited, or whose mode drifted, is replaced whole.
func TestUnchangedFileNotRewritten(t *testing.T) {
	z, roots := newTestZip(t)
	path := filepath.Join(roots, "etc", "settings.json")
	addRootless(z, placement.File{Paths: map[string]string{testOS(): path}, Template: `{"a": 1}`})
	regenerate := func(step string) os.FileInfo {
		t.Helper()
		if failed := z.RegenerateAll(); len(failed) != 0 {
			t.Fatalf("%s: RegenerateAll = %+v", step, failed)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	first := regenerate("first")
	if again := regenerate("again"); !os.SameFile(first, again) {
		t.Error("an unchanged file was rewritten")
	}
	want := readFile(t, path)
	if err := os.WriteFile(path, []byte(`{"a": 2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if regenerate("edited"); readFile(t, path) != want {
		t.Errorf("edited file = %q, want %q", readFile(t, path), want)
	}
	if !osHasModes() {
		return
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	after := regenerate("mode drift")
	if after.Mode() != 0o644 || os.SameFile(before, after) {
		t.Errorf("after mode drift: mode %v, same file %t; want 0644 through a rewrite", after.Mode(), os.SameFile(before, after))
	}
}
