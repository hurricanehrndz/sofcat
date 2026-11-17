package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/process"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
)

// stubPlacedItems serves Skill, a zip item in placements a, b and c on
// Windows, Solo, a zip item in a only, and Tool, which is not a zip item, all
// offered, with Required also in managed_installs. It returns the config and
// the write root the placements live under.
func stubPlacedItems(t *testing.T) (config.Configuration, string) {
	t.Helper()
	const goos = placement.Windows
	roots := t.TempDir()
	cfg := config.Configuration{AppDataPath: t.TempDir(), CachePath: t.TempDir(), Catalogs: []string{"production"}}
	table := placement.Table{
		Placements: map[string]placement.Spec{
			"a": {Paths: map[string]string{goos: filepath.Join(roots, "a")}, AppliesTo: []string{"Claude Code"}},
			"b": {Paths: map[string]string{goos: filepath.Join(roots, "b")}, AppliesTo: []string{"Codex", "Cursor"}},
			"c": {Paths: map[string]string{goos: filepath.Join(roots, "c")}},
		},
		Order: []string{"a", "b", "c"},
	}
	zipItem := func(name string, placements ...string) catalog.Item {
		variants := map[string]map[string]catalog.Variant{}
		for _, p := range placements {
			variants[p] = map[string]catalog.Variant{goos: {}}
		}
		return catalog.Item{Name: name, Installer: catalog.InstallerItem{Type: "zip", Variants: variants}}
	}
	catalogs := map[int]map[string]catalog.Item{1: {
		"Skill":    zipItem("Skill", "a", "b", "c"),
		"Required": zipItem("Required", "a", "b", "c"),
		"Solo":     zipItem("Solo", "a"),
		"Tool":     {Name: "Tool", Installer: catalog.InstallerItem{Type: "ps1"}},
	}}
	stubZipList(t, catalogs, func(config.Configuration) (placement.Table, error) { return table, nil })
	manifestGet = func(config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{{Name: "site", Installs: []string{"Required"}, OptionalInstalls: []string{"Skill", "Required", "Solo", "Tool"}}}, nil, nil
	}
	newZipInstaller = func(cfg config.Configuration, table placement.Table) *installer.ZipInstaller {
		z := installer.NewMachineZip(cfg, table)
		z.Guard = placement.Guard{GOOS: goos, Roots: placement.Roots{Windows: []string{roots}}, DataDir: cfg.AppDataPath}
		return z
	}
	return cfg, roots
}

func saveSelfServe(t *testing.T, cfg config.Configuration, entry manifest.Item) {
	t.Helper()
	if err := manifest.SaveSelfServe(serviceLocalManifestPath(cfg), entry); err != nil {
		t.Fatal(err)
	}
}

// A request for one placement adds it to, or takes it from, the item's
// placement_selections; a set covering every placement, or none, falls back
// to what a request for the whole item does, so the file never holds a
// subset that means "everywhere" or "nowhere".
func TestPlacementSelectionTransitions(t *testing.T) {
	everywhere := manifest.Item{Installs: []string{"Skill"}}
	narrowed := func(p ...string) manifest.Item {
		return manifest.Item{Installs: []string{"Skill"}, PlacementSelections: map[string][]string{"Skill": p}}
	}
	removed := manifest.Item{Uninstalls: []string{"Skill"}}
	cases := []struct {
		name      string
		before    manifest.Item
		remove    bool
		placement string
		want      manifest.Item
	}{
		{name: "install one of an unselected item", placement: "b", want: narrowed("b")},
		{name: "install adds to the subset", before: narrowed("b"), placement: "a", want: narrowed("a", "b")},
		{name: "a subset covering everything is cleared", before: narrowed("a", "b"), placement: "c", want: everywhere},
		{name: "install one of an item selected everywhere", before: everywhere, placement: "a", want: everywhere},
		{name: "install of the whole item clears the subset", before: narrowed("a"), want: everywhere},
		{name: "install one over a pending removal", before: removed, placement: "c", want: narrowed("c")},
		{name: "remove one of an item selected everywhere", before: everywhere, remove: true, placement: "a", want: narrowed("b", "c")},
		{name: "remove one of a subset", before: narrowed("a", "c"), remove: true, placement: "c", want: narrowed("a")},
		{name: "an empty subset is a full removal", before: narrowed("b"), remove: true, placement: "b", want: removed},
		{name: "remove of the whole item clears the subset", before: narrowed("a"), remove: true, want: removed},
		{name: "remove one of an unselected item is a full removal", remove: true, placement: "a", want: removed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := stubPlacedItems(t)
			saveSelfServe(t, cfg, tc.before)
			var err error
			if tc.remove {
				_, _, err = removeServiceManagedInstall(cfg, testAdmin, "Skill", tc.placement)
			} else {
				_, _, _, err = addServiceManagedInstall(cfg, testAdmin, "Skill", tc.placement)
			}
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			got := loadManifest(t, cfg)
			if !slices.Equal(got.Installs, tc.want.Installs) || !slices.Equal(got.Uninstalls, tc.want.Uninstalls) ||
				!slices.Equal(got.PlacementSelections["Skill"], tc.want.PlacementSelections["Skill"]) {
				t.Fatalf("installs=%v uninstalls=%v placements=%v, want %v/%v/%v", got.Installs, got.Uninstalls, got.PlacementSelections,
					tc.want.Installs, tc.want.Uninstalls, tc.want.PlacementSelections)
			}
		})
	}
}

// A placement the item does not have here, or one named for an item that is
// not a zip item, is refused as invalid params before anything is written.
func TestPlacementSelectionRefusesUnknownPlacements(t *testing.T) {
	cases := []struct {
		item, placement string
		remove          bool
	}{
		{item: "Skill", placement: "nowhere"},
		{item: "Skill", placement: "nowhere", remove: true},
		{item: "Solo", placement: "b"},
		{item: "Tool", placement: "a"},
	}
	for _, tc := range cases {
		// A zip-only build does not offer Tool at all: item_not_available.
		if tc.item == "Tool" && process.ZipOnlyReason(catalog.Item{Installer: catalog.InstallerItem{Type: "ps1"}}) != "" {
			continue
		}
		cfg, _ := stubPlacedItems(t)
		saveSelfServe(t, cfg, manifest.Item{Installs: []string{tc.item}})
		var err error
		if tc.remove {
			_, _, err = removeServiceManagedInstall(cfg, testAdmin, tc.item, tc.placement)
		} else {
			_, _, _, err = addServiceManagedInstall(cfg, testAdmin, tc.item, tc.placement)
		}
		if !errors.Is(err, errInvalidPlacement) || !IsErrorCode(commandError(err, ""), "invalid_params") {
			t.Errorf("%s in %s (remove %v): err = %v, want invalid_params", tc.item, tc.placement, tc.remove, err)
		}
		if got := loadManifest(t, cfg); !slices.Equal(got.Installs, []string{tc.item}) || len(got.Uninstalls) != 0 || got.PlacementSelections != nil {
			t.Errorf("%s in %s: a refused request changed the file: %+v", tc.item, tc.placement, got)
		}
	}
}

// Taking a placement away is a removal (R32): only the selector or an
// administrator may, and never for an item an admin manifest requires.
// Adding one back is an install, which anyone may ask for.
func TestPlacementNarrowingIsARemoval(t *testing.T) {
	cfg, _ := stubPlacedItems(t)
	saveSelfServe(t, cfg, manifest.Item{Installs: []string{"Skill"}, SelectedBy: map[string]string{"Skill": userA.ID}})

	if _, _, err := removeServiceManagedInstall(cfg, userB, "Skill", "a"); !errors.Is(err, errNotAuthorized) {
		t.Fatalf("another user's narrowing: err = %v, want errNotAuthorized", err)
	}
	if got := loadManifest(t, cfg); got.PlacementSelections != nil {
		t.Fatalf("a refused narrowing changed the file: %+v", got)
	}
	if _, _, err := removeServiceManagedInstall(cfg, userA, "Skill", "a"); err != nil {
		t.Fatalf("the selector's narrowing: %v", err)
	}
	if _, _, _, err := addServiceManagedInstall(cfg, userB, "Skill", "a"); err != nil {
		t.Fatalf("another user's widening: %v", err)
	}
	got := loadManifest(t, cfg)
	if got.PlacementSelections != nil || got.SelectedBy["Skill"] != userA.ID {
		t.Fatalf("after widening: placements=%v selectedBy=%v, want cleared and still %s", got.PlacementSelections, got.SelectedBy, userA.ID)
	}

	if _, _, err := removeServiceManagedInstall(cfg, testAdmin, "Required", "a"); !errors.Is(err, errItemNotRemovable) {
		t.Fatalf("narrowing a required item: err = %v, want errItemNotRemovable", err)
	}
}

// A cancel puts back the subset the request replaced, and a refused cancel
// keeps the one the request set.
func TestWithdrawItemRestoresPlacementSelection(t *testing.T) {
	cfg, _ := stubPlacedItems(t)
	saveSelfServe(t, cfg, manifest.Item{Installs: []string{"Skill"}, PlacementSelections: map[string][]string{"Skill": {"a", "b"}}})
	prior, requested, err := removeServiceManagedInstall(cfg, testAdmin, "Skill", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := loadManifest(t, cfg).PlacementSelections["Skill"]; !slices.Equal(got, []string{"b"}) {
		t.Fatalf("after remove: %v, want [b]", got)
	}
	if err := withdrawItem(cfg, installer.NewCancels(), "Skill", prior, requested); err != nil {
		t.Fatal(err)
	}
	if got := loadManifest(t, cfg); !slices.Equal(got.PlacementSelections["Skill"], []string{"a", "b"}) || !slices.Equal(got.Installs, []string{"Skill"}) {
		t.Fatalf("after cancel: %+v, want Skill selected for [a b]", got)
	}

	if err := withdrawItem(cfg, nil, "Skill", prior, requested); !errors.Is(err, errNotCancelable) {
		t.Fatalf("refused cancel: err = %v", err)
	}
	if got := loadManifest(t, cfg).PlacementSelections["Skill"]; !slices.Equal(got, []string{"b"}) {
		t.Fatalf("after a refused cancel: %v, want the request's [b]", got)
	}
}

// The list carries placements only for a zip item with two or more of them
// here, with labels and per-placement statuses, and judges a narrowed item
// over its chosen placements: Skill, narrowed to a and placed in a and b, is
// installed, and its b tree will be removed. Required ignores its subset.
func TestListPlacements(t *testing.T) {
	cfg, roots := stubPlacedItems(t)
	for _, place := range []struct{ item, placement string }{{"Skill", "a"}, {"Skill", "b"}, {"Required", "a"}} {
		tree := filepath.Join(roots, place.placement, place.item)
		if err := os.MkdirAll(tree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, "SKILL.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := receipt.Write(receipt.MachineDir(cfg.AppDataPath), receipt.Receipt{Item: place.item, Placement: place.placement, Path: tree}); err != nil {
			t.Fatal(err)
		}
	}
	saveSelfServe(t, cfg, manifest.Item{
		Installs:            []string{"Required", "Skill"},
		PlacementSelections: map[string][]string{"Skill": {"a"}, "Required": {"a"}},
	})

	items, err := getOptionalItems(cfg, peer{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]OptionalInstallItem{}
	for _, it := range items {
		byName[it.ItemName] = it
	}

	skill := byName["Skill"]
	if !skill.IsInstalled || skill.Status != "Installed" {
		t.Errorf("Skill isInstalled=%v status=%s, want installed over its subset", skill.IsInstalled, skill.Status)
	}
	line, err := json.Marshal(skill.Placements)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"name":"a","label":"Claude Code","isInstalled":true,"status":"Installed"},` +
		`{"name":"b","label":"Codex, Cursor","isInstalled":true,"status":"WillBeRemoved"},` +
		`{"name":"c","label":"c","isInstalled":false,"status":"NotInstalled"}]`
	if string(line) != want {
		t.Errorf("Skill placements = %s\nwant %s", line, want)
	}

	required := byName["Required"]
	if required.IsInstalled || required.Placements[1].Status != "WillBeInstalled" {
		t.Errorf("Required = %+v, want judged over every placement despite its subset", required)
	}
	for _, name := range []string{"Solo", "Tool"} {
		line, err := json.Marshal(byName[name])
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(line, &fields)
		if _, ok := fields["placements"]; ok {
			t.Errorf("%s has placements %s, want the key left out", name, fields["placements"])
		}
	}
}

// -S InstallItem:item:placement names one placement; without it the command
// is for every placement, as before.
func TestParseCommandSpecPlacement(t *testing.T) {
	cmd, err := parseCommandSpec("InstallItem: Skill : claude ")
	if err != nil || cmd.Items[0] != "Skill" || cmd.Placement != "claude" {
		t.Fatalf("InstallItem with placement = %#v, %v", cmd, err)
	}
	cmd, err = parseCommandSpec("RemoveItem:Skill")
	if err != nil || cmd.Items[0] != "Skill" || cmd.Placement != "" {
		t.Fatalf("RemoveItem without placement = %#v, %v", cmd, err)
	}
	if _, err := parseCommandSpec("RemoveItem::claude"); err == nil {
		t.Fatal("RemoveItem without an item was accepted")
	}
}
