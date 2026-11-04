package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
)

// TestMain stubs the harness table: every list call fetches it, and the tests
// must not reach for a network repository. Tests that need a table set their
// own.
func TestMain(m *testing.M) {
	harnessGet = func(config.Configuration) (harness.Table, error) { return harness.Table{}, nil }
	os.Exit(m.Run())
}

// TestListZipItemStatus: a zip item has no check block, so the list call
// must read its receipts and placed tree rather than report Unknown. A tree
// the user edited is still installed: sofcat tracks it and can remove it.
func TestListZipItemStatus(t *testing.T) {
	const goos = harness.Windows // works on any host; see cmd/sofcat/tapas_test.go
	appData, roots := t.TempDir(), t.TempDir()
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"production"}}
	root := filepath.Join(roots, "skills")
	table := harness.Table{
		Placements: map[string]harness.Placement{"skills": {Kind: harness.KindDirectory, Paths: map[string]string{goos: root}}},
		Order:      []string{"skills"},
	}

	// Place "Placed" and "Edited" as a run would have: tree and receipt.
	// Then edit "Edited" so it matches neither the catalog nor its receipt.
	var hash string
	for _, name := range []string{"Placed", "Edited"} {
		tree := filepath.Join(root, name)
		if err := os.MkdirAll(tree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, "SKILL.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		h, err := artifact.TreeHash(tree, goos)
		if err != nil {
			t.Fatal(err)
		}
		hash = h
		if err = receipt.Write(receipt.MachineDir(appData), receipt.Receipt{Item: name, Placement: "skills", Path: tree, TreeHash: h}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Edited", "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	zipItem := func(name string) catalog.Item {
		return catalog.Item{Name: name, Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{
			"skills": {goos: {Location: name + ".zip", Hash: "00", TreeHash: hash}},
		}}}
	}
	catalogs := map[int]map[string]catalog.Item{1: {"Placed": zipItem("Placed"), "Edited": zipItem("Edited"), "Absent": zipItem("Absent")}}

	stubZipList(t, catalogs, func(config.Configuration) (harness.Table, error) { return table, nil })
	newZipInstaller = func(cfg config.Configuration, table harness.Table) *installer.ZipInstaller {
		z := installer.NewMachineZip(cfg, table)
		z.Guard = placement.Guard{GOOS: goos, Roots: placement.Roots{Windows: []string{roots}}, DataDir: appData}
		return z
	}

	items, err := getOptionalItems(cfg)
	if err != nil {
		t.Fatalf("getOptionalItems: %v", err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.ItemName] = it.Status
	}
	if got["Placed"] != "Installed" || got["Edited"] != "Installed" || got["Absent"] != "NotInstalled" {
		t.Errorf("statuses = %v, want Placed and Edited Installed, Absent NotInstalled", got)
	}
}

// TestListHarnessError: the list call always fetches the harness table, and a
// failure fails the call like a catalog failure rather than guessing statuses.
func TestListHarnessError(t *testing.T) {
	catalogs := map[int]map[string]catalog.Item{1: {"Tool": {Name: "Tool", Installer: catalog.InstallerItem{Type: "ps1"}}}}
	wantErr := errors.New("harnesses.yaml unreachable")
	stubZipList(t, catalogs, func(config.Configuration) (harness.Table, error) { return harness.Table{}, wantErr })
	cfg := config.Configuration{AppDataPath: t.TempDir(), Catalogs: []string{"production"}}
	if _, err := getOptionalItems(cfg); !errors.Is(err, wantErr) {
		t.Errorf("getOptionalItems err = %v, want the harness error", err)
	}
}

// stubZipList serves one manifest offering every catalog item, the catalogs,
// and the harness table from get, restoring the hooks afterwards.
func stubZipList(t *testing.T, catalogs map[int]map[string]catalog.Item, get func(config.Configuration) (harness.Table, error)) {
	t.Helper()
	origManifest, origCatalog, origHarness, origZip := manifestGet, catalogGet, harnessGet, newZipInstaller
	t.Cleanup(func() {
		manifestGet, catalogGet, harnessGet, newZipInstaller = origManifest, origCatalog, origHarness, origZip
	})
	var offered []string
	for _, items := range catalogs {
		for name := range items {
			offered = append(offered, name)
		}
	}
	manifestGet = func(config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{{Name: "site", OptionalInstalls: offered}}, nil, nil
	}
	catalogGet = func(config.Configuration) (map[int]map[string]catalog.Item, error) { return catalogs, nil }
	harnessGet = get
}

// TestListStorefrontFields: the list call carries each item's resolved labels
// and works-in harnesses, and an item with neither on this OS sends neither
// key, so the UI falls back to its built-in labels.
func TestListStorefrontFields(t *testing.T) {
	const goos = harness.Windows
	table := harness.Table{
		Placements: map[string]harness.Placement{
			"skills": {Kind: harness.KindDirectory, WorksIn: []string{"Claude Code"}, ActionLabels: map[string]string{"install": "Add", "installed": "Added"}},
			"codex":  {Kind: harness.KindDirectory, WorksIn: []string{"Codex", "Claude Code"}, ActionLabels: map[string]string{"install": "Place"}},
		},
		Order: []string{"skills", "codex"},
	}
	variants := map[string]map[string]catalog.Variant{"codex": {goos: {}}, "skills": {goos: {}}}
	catalogs := map[int]map[string]catalog.Item{1: {
		"Skill": {Name: "Skill", CustomActionLabels: map[string]string{"installed": "Ready"}, Installer: catalog.InstallerItem{Type: "zip", Variants: variants}},
		// Its only variant is for another OS, so none of its placements count.
		"Tool": {Name: "Tool", Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{"skills": {harness.Darwin: {}}}}},
	}}
	stubZipList(t, catalogs, func(config.Configuration) (harness.Table, error) { return table, nil })
	newZipInstaller = func(cfg config.Configuration, table harness.Table) *installer.ZipInstaller {
		z := installer.NewMachineZip(cfg, table)
		z.Guard = placement.Guard{GOOS: goos, DataDir: cfg.AppDataPath}
		return z
	}

	items, err := getOptionalItems(config.Configuration{AppDataPath: t.TempDir(), CachePath: t.TempDir(), Catalogs: []string{"production"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		line, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(line, &fields)
		got[it.ItemName] = string(fields["actionLabels"]) + " " + string(fields["worksIn"])
	}
	if want := `{"install":"Add","installed":"Ready"} ["Claude Code","Codex"]`; got["Skill"] != want {
		t.Errorf("Skill = %s, want %s", got["Skill"], want)
	}
	if got["Tool"] != " " {
		t.Errorf("Tool = %q, want no actionLabels or worksIn keys", got["Tool"])
	}
}
