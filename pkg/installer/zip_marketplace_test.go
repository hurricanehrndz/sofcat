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

// marketTest is an installer with one write root holding a marketplace
// placement "market" (two manifest files) and a settings placement "drop-in"
// that registers it, parsed from YAML as a repository would serve it.
type marketTest struct {
	z        *ZipInstaller
	root     string // the marketplace root
	settings string // the drop-in's path
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
    kind: marketplace
    paths: {%[1]s: '%[2]s'}
    exclusive: true
    marketplace:
      name: acme
      owner: {name: Acme IT, email: it@acme.test}
      renames:
        old-plugin: new-plugin
        older: {to: new-plugin, since: 2, keep: true, aliases: [a, b]}
      allowCrossMarketplaceDependenciesOn: [other, {name: third, only: [x]}]
      manifest_files: [.claude-plugin/marketplace.json, .agents/plugins/marketplace.json]
  drop-in:
    kind: settings
    paths: {%[1]s: '%[3]s'}
    registers: market
`, testOS(), root, settings))
	if err != nil {
		t.Fatal(err)
	}
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

// TestMarketplaceRegenerateRefusalHeals: a settings path refused on install
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
	if f := r.Report.FailedItems[0]; f.Name != "market" || f.Action != "generate" || f.Code != report.CodePlacementRefused || !strings.Contains(f.Error, `"drop-in"`) {
		t.Errorf("failed item = %+v, want market generate placement_refused naming drop-in", f)
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

// TestSettingsPlacementRefusedAsTarget: a settings placement is a generated
// file; a variant naming it would write a tree where the file goes.
func TestSettingsPlacementRefusedAsTarget(t *testing.T) {
	m := newMarketTest(t, "")
	item, archive := plugin(t, "sneaky", "drop-in")
	res := m.z.Apply(item, target(t, m.z, item, "drop-in"), archive)
	if res.Outcome != OutcomeRefused || res.Code() != report.CodePlacementRefused {
		t.Errorf("settings variant = %s %q (%v), want refused", res.Outcome, res.Code(), res.Err)
	}
	if exists(filepath.Dir(m.settings)) {
		t.Error("a refused settings variant wrote files")
	}
}

// TestSettingsPathOutsideRootsRefused: the drop-in's path passes the same
// guard as any placement. The plugin is placed, but the item fails with
// placement_refused naming the settings placement, and nothing is written
// outside the roots.
func TestSettingsPathOutsideRootsRefused(t *testing.T) {
	outside := filepath.Join(tempDir(t), "settings.d", "50-acme.json")
	m := newMarketTest(t, outside)
	item, archive := plugin(t, "one", "market")
	res := m.z.Apply(item, target(t, m.z, item, "market"), archive)
	if res.Outcome != OutcomeRefused || res.Code() != report.CodePlacementRefused || !strings.Contains(res.Err.Error(), `"drop-in"`) {
		t.Errorf("result = %s %q (%v), want refused naming drop-in", res.Outcome, res.Code(), res.Err)
	}
	if exists(filepath.Dir(outside)) {
		t.Error("wrote outside the write roots")
	}
	if !strings.Contains(m.manifest(t), `"./plugins/one"`) {
		t.Error("manifest not written beside the refusal")
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
