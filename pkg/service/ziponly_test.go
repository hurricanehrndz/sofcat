//go:build zip_only

package service

import (
	"errors"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// A zip-only build cannot install an item that needs a command installer or a
// catalog script (spec R21), so the service must neither list it nor accept
// an InstallItem for it.
func TestZipOnlyRejectedItemsNotOffered(t *testing.T) {
	cfg := config.Configuration{AppDataPath: t.TempDir(), CachePath: t.TempDir(), Catalogs: []string{"production"}}
	zip := catalog.Item{Name: "Zip", Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{
		"skills": {"windows": {Location: "z.zip"}},
	}}}
	scripted := zip
	scripted.Name, scripted.PostScript = "Scripted", "exit 0"
	exe := catalog.Item{Name: "Exe", Installer: catalog.InstallerItem{Type: "exe", Location: "a.exe"}}
	stubZipList(t, map[int]map[string]catalog.Item{1: {"Zip": zip, "Scripted": scripted, "Exe": exe}},
		func(config.Configuration) (placement.Table, error) { return placement.Table{}, nil })

	items, err := getOptionalItems(cfg, peer{})
	if err != nil {
		t.Fatalf("getOptionalItems: %v", err)
	}
	if len(items) != 1 || items[0].ItemName != "Zip" {
		t.Errorf("listed %+v; want only Zip", items)
	}
	for _, name := range []string{"Scripted", "Exe"} {
		if _, _, err := addServiceManagedInstall(cfg, testAdmin, name); !errors.Is(err, errItemNotAvailable) {
			t.Errorf("addServiceManagedInstall(%s) error = %v, want errItemNotAvailable", name, err)
		}
	}
}
