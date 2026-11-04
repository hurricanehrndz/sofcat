//go:build !zip_only

// Optional-item listing with command-installer fixtures, which a zip-only
// build rejects (process.ZipOnlyReason).

package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
)

// TestGetOptionalItemsHonestStatus exercises the enriched ListOptionalInstalls
// payload (R9): every status branch plus the script-only and no-catalog fallbacks
// to Unknown, driven by real file checks through one shared status.Checker.
func TestGetOptionalItemsHonestStatus(t *testing.T) {
	appData := filepath.Clean(t.TempDir())
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"selfserve"}}

	// Two markers on disk make "installed" real; the missing ones are not.
	present := filepath.Join(t.TempDir(), "present.txt")
	if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	presentRemove := filepath.Join(t.TempDir(), "present-remove.txt")
	if err := os.WriteFile(presentRemove, []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")

	fileCheck := func(path string) catalog.InstallCheck {
		return catalog.InstallCheck{File: []catalog.FileCheck{{Path: path}}}
	}

	origManifest, origCatalog := manifestGet, catalogGet
	t.Cleanup(func() { manifestGet, catalogGet = origManifest, origCatalog })
	manifestGet = func(_ config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{{Name: "base", OptionalInstalls: []string{
			"Installed", "PendingRemove", "WillInstall", "NotInstalled", "ScriptOnly", "NoCatalog",
		}}}, nil, nil
	}
	catalogGet = func(_ config.Configuration) (map[int]map[string]catalog.Item, error) {
		return map[int]map[string]catalog.Item{1: {
			"Installed":     {Name: "Installed", DisplayName: "Installed App", Version: "1.0", Description: "d", Category: "c", Developer: "dev", Check: fileCheck(present)},
			"PendingRemove": {Name: "PendingRemove", Check: fileCheck(presentRemove)},
			"WillInstall":   {Name: "WillInstall", Check: fileCheck(missing)},
			"NotInstalled":  {Name: "NotInstalled", Check: fileCheck(missing)},
			"ScriptOnly":    {Name: "ScriptOnly", Check: catalog.InstallCheck{Script: "exit 0"}},
		}}, nil
	}

	// Selected + pending-removal self-serve state.
	ssPath := manifest.SelfServePath(appData)
	if err := manifest.SaveSelfServe(ssPath, manifest.Item{Installs: []string{"WillInstall"}, Uninstalls: []string{"PendingRemove"}}); err != nil {
		t.Fatalf("save self-serve: %v", err)
	}

	items, err := getOptionalItems(cfg)
	if err != nil {
		t.Fatalf("getOptionalItems failed: %v", err)
	}

	byName := make(map[string]OptionalInstallItem, len(items))
	for _, it := range items {
		byName[it.ItemName] = it
	}
	wantStatus := map[string]string{
		"Installed":     "Installed",
		"PendingRemove": "WillBeRemoved",
		"WillInstall":   "WillBeInstalled",
		"NotInstalled":  "NotInstalled",
		"ScriptOnly":    "Unknown",
		"NoCatalog":     "Unknown",
	}
	for name, want := range wantStatus {
		got, ok := byName[name]
		if !ok {
			t.Errorf("missing item %q in payload", name)
			continue
		}
		if got.Status != want {
			t.Errorf("item %q status = %q, want %q", name, got.Status, want)
		}
	}
	if byName["Installed"].Catalog != "selfserve" || byName["Installed"].Description != "d" {
		t.Errorf("Installed item missing catalog/metadata: %#v", byName["Installed"])
	}
	if byName["WillInstall"].IsManaged != true {
		t.Errorf("WillInstall should be managed (selected)")
	}
	if byName["ScriptOnly"].IsInstalled {
		t.Errorf("script-only item must report isInstalled=false")
	}
}

// An optional install this machine's installable_condition rejects is not
// offered: it is left out of the list and installItem refuses it. A variant in
// a later catalog that the machine passes is offered instead.
func TestGetOptionalItemsNotInstallable(t *testing.T) {
	appData := filepath.Clean(t.TempDir())
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"first", "second"}}

	origManifest, origCatalog := manifestGet, catalogGet
	t.Cleanup(func() { manifestGet, catalogGet = origManifest, origCatalog })
	manifestGet = func(_ config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{{Name: "base", OptionalInstalls: []string{"Anywhere", "Nowhere", "Variant"}}}, nil, nil
	}
	never := `hostname == "no-such-host.invalid"`
	catalogGet = func(_ config.Configuration) (map[int]map[string]catalog.Item, error) {
		return map[int]map[string]catalog.Item{
			1: {
				"Anywhere": {Name: "Anywhere"},
				"Nowhere":  {Name: "Nowhere", InstallableCondition: never},
				"Variant":  {Name: "Variant", DisplayName: "Variant A", InstallableCondition: never},
			},
			2: {"Variant": {Name: "Variant", DisplayName: "Variant B"}},
		}, nil
	}

	items, err := getOptionalItems(cfg)
	if err != nil {
		t.Fatalf("getOptionalItems failed: %v", err)
	}
	got := map[string]OptionalInstallItem{}
	for _, it := range items {
		got[it.ItemName] = it
	}
	if _, ok := got["Nowhere"]; ok || len(got) != 2 {
		t.Errorf("listed %v; want Anywhere and Variant only", got)
	}
	if v := got["Variant"]; v.DisplayName != "Variant B" || v.Catalog != "second" {
		t.Errorf("Variant = %q from %q; want Variant B from second", v.DisplayName, v.Catalog)
	}

	if _, _, err := addServiceManagedInstall(cfg, testAdmin, "Nowhere"); !errors.Is(err, errItemNotAvailable) {
		t.Errorf("addServiceManagedInstall(Nowhere) error = %v, want errItemNotAvailable", err)
	}
}
