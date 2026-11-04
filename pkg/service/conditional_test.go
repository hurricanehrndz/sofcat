//go:build !zip_only

package service

import (
	"fmt"
	"maps"
	"runtime"
	"slices"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/facts"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/process"
)

// The list call and a managed run must choose from the same expanded
// manifests, or the UI offers an item a run will not install, or a run removes
// an item the UI still shows.
func TestListAndRunAgreeOnConditionalItems(t *testing.T) {
	appData := t.TempDir()
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"production"}}
	raw := []manifest.Item{{
		Name:             "site",
		OptionalInstalls: []string{"Plain"},
		ConditionalItems: []manifest.ConditionalItem{
			{
				Condition:        fmt.Sprintf("os == %q", runtime.GOOS),
				Installs:         []string{"RequiredHere"},
				OptionalInstalls: []string{"OptionalHere"},
				ConditionalItems: []manifest.ConditionalItem{{Condition: `os == "plan9"`, OptionalInstalls: []string{"NestedFalse"}}},
			},
			{Condition: `os == "plan9"`, Installs: []string{"RequiredElsewhere"}, OptionalInstalls: []string{"OptionalElsewhere"}},
			{Condition: `"lab" in machine_groups`, Installs: []string{"LabRequired"}, OptionalInstalls: []string{"LabOptional"}, Uninstalls: []string{"LabRemoved"}},
		},
	}}
	catalogs := map[int]map[string]catalog.Item{1: {}}
	for _, name := range []string{"Plain", "RequiredHere", "OptionalHere", "NestedFalse", "RequiredElsewhere", "OptionalElsewhere", "LabRequired", "LabOptional", "LabRemoved"} {
		catalogs[1][name] = catalog.Item{Name: name, Installer: catalog.InstallerItem{Type: "msi", Location: name + ".msi"}}
	}

	origManifest, origCatalog := manifestGet, catalogGet
	t.Cleanup(func() { manifestGet, catalogGet = origManifest, origCatalog })
	manifestGet = func(config.Configuration) ([]manifest.Item, []string, error) { return raw, nil, nil }
	catalogGet = func(config.Configuration) (map[int]map[string]catalog.Item, error) { return catalogs, nil }

	listed, err := getOptionalItems(cfg)
	if err != nil {
		t.Fatalf("getOptionalItems: %v", err)
	}
	var offered []string
	for _, it := range listed {
		offered = append(offered, it.ItemName)
	}
	serviceManifests, _, _, err := adminManifests(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// The managed run: the same manifests and facts, through the run's list
	// building. A user who selected every optional name gets only the
	// authorized ones.
	runManifests, held := manifest.Expand(raw, facts.Collect(facts.Dir(appData), false))
	installs, uninstalls, updates := process.Manifests(runManifests, catalogs)
	var selfServe manifest.Item
	for name := range catalogs[1] {
		selfServe.Installs = append(selfServe.Installs, name)
	}
	authorized, _, _ := process.ReconcileSelfServe(&selfServe, runManifests)
	slices.Sort(authorized)

	if want := []string{"OptionalHere", "Plain"}; !slices.Equal(offered, want) || !slices.Equal(authorized, want) {
		t.Errorf("list offers %v, run authorizes %v; want both %v", offered, authorized, want)
	}
	if required := slices.Sorted(maps.Keys(requiredItems(serviceManifests))); !slices.Equal(required, installs) || !slices.Equal(installs, []string{"RequiredHere"}) {
		t.Errorf("service requires %v, run installs %v; want both [RequiredHere]", required, installs)
	}
	// The unknown block's removal is held, not carried out.
	if len(uninstalls) != 0 || len(updates) != 0 {
		t.Errorf("run uninstalls %v, updates %v; want none", uninstalls, updates)
	}
	if len(held) != 1 || !slices.Equal(held[0].Items, []string{"LabRequired", "LabOptional", "LabRemoved"}) {
		t.Errorf("held = %+v; want the lab block's items", held)
	}
}
