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
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
)

// TestMain stubs the placement table: every list call fetches it, and the tests
// must not reach for a network repository. Tests that need a table set their
// own.
func TestMain(m *testing.M) {
	placementsGet = func(config.Configuration) (placement.Table, error) { return placement.Table{}, nil }
	os.Exit(m.Run())
}

// TestListZipItemStatus: a zip item has no check block, so the list call
// must read its receipts and placed tree rather than report Unknown. A tree
// the user edited is still installed: sofcat tracks it and can remove it.
func TestListZipItemStatus(t *testing.T) {
	const goos = placement.Windows // works on any host; see cmd/sofcat/tapas_test.go
	appData, roots := t.TempDir(), t.TempDir()
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"production"}}
	root := filepath.Join(roots, "skills")
	table := placement.Table{
		Placements: map[string]placement.Spec{"skills": {Paths: map[string]string{goos: root}}},
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

	stubZipList(t, catalogs, func(config.Configuration) (placement.Table, error) { return table, nil })
	newZipInstaller = func(cfg config.Configuration, table placement.Table) *installer.ZipInstaller {
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

// TestListPlacementsError: the list call always fetches the placement table, and a
// failure fails the call like a catalog failure rather than guessing statuses.
func TestListPlacementsError(t *testing.T) {
	catalogs := map[int]map[string]catalog.Item{1: {"Tool": {Name: "Tool", Installer: catalog.InstallerItem{Type: "ps1"}}}}
	wantErr := errors.New("placements.yaml unreachable")
	stubZipList(t, catalogs, func(config.Configuration) (placement.Table, error) { return placement.Table{}, wantErr })
	cfg := config.Configuration{AppDataPath: t.TempDir(), Catalogs: []string{"production"}}
	if _, err := getOptionalItems(cfg); !errors.Is(err, wantErr) {
		t.Errorf("getOptionalItems err = %v, want the placements error", err)
	}
}

// stubZipList serves one manifest offering every catalog item, the catalogs,
// and the placement table from get, restoring the hooks afterwards.
func stubZipList(t *testing.T, catalogs map[int]map[string]catalog.Item, get func(config.Configuration) (placement.Table, error)) {
	t.Helper()
	origManifest, origCatalog, origPlacements, origZip := manifestGet, catalogGet, placementsGet, newZipInstaller
	t.Cleanup(func() {
		manifestGet, catalogGet, placementsGet, newZipInstaller = origManifest, origCatalog, origPlacements, origZip
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
	placementsGet = get
}

// TestListStorefrontFields: the list call carries each item's section (its
// storefront tab), resolved labels and applies_to names, and an item with
// none of them on this OS sends none of the keys, so the UI falls back to its
// built-in tab and labels.
func TestListStorefrontFields(t *testing.T) {
	const goos = placement.Windows
	table := placement.Table{
		Placements: map[string]placement.Spec{
			"skills": {AppliesTo: []string{"Claude Code"}, ActionLabels: map[string]string{"install": "Add", "installed": "Added"}},
			"codex":  {AppliesTo: []string{"Codex", "Claude Code"}, ActionLabels: map[string]string{"install": "Place"}},
		},
		Order: []string{"skills", "codex"},
	}
	variants := map[string]map[string]catalog.Variant{"codex": {goos: {}}, "skills": {goos: {}}}
	catalogs := map[int]map[string]catalog.Item{1: {
		"Skill": {Name: "Skill", Section: "Skills", CustomActionLabels: map[string]string{"installed": "Ready"}, Installer: catalog.InstallerItem{Type: "zip", Variants: variants}},
		// Its only variant is for another OS, so none of its placements count.
		"Tool": {Name: "Tool", Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{"skills": {placement.Darwin: {}}}}},
	}}
	stubZipList(t, catalogs, func(config.Configuration) (placement.Table, error) { return table, nil })
	newZipInstaller = func(cfg config.Configuration, table placement.Table) *installer.ZipInstaller {
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
		got[it.ItemName] = string(fields["section"]) + " " + string(fields["actionLabels"]) + " " + string(fields["appliesTo"])
	}
	if want := `"Skills" {"install":"Add","installed":"Ready"} ["Claude Code","Codex"]`; got["Skill"] != want {
		t.Errorf("Skill = %s, want %s", got["Skill"], want)
	}
	if got["Tool"] != "  " {
		t.Errorf("Tool = %q, want no section, actionLabels or appliesTo keys", got["Tool"])
	}
}
