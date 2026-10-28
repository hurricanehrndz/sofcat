package process

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// conditionCatalogs models a machine where conditions.Apply rejected some
// items: Chrome has an arm64 variant in catalog 1 and an x64 one in catalog 2.
func conditionCatalogs() map[int]map[string]catalog.Item {
	pkg := func(display, notInstallable string, deps ...string) catalog.Item {
		return catalog.Item{
			Name:           display,
			DisplayName:    display,
			Installer:      catalog.InstallerItem{Type: "msi", Location: display + ".msi"},
			Uninstaller:    catalog.InstallerItem{Type: "msi", Location: display + ".msi"},
			Dependencies:   deps,
			NotInstallable: notInstallable,
		}
	}
	arm := pkg("ChromeArm", `installable_condition "arch == \"arm64\"" is false`)
	arm.Name = "Chrome"
	x64 := pkg("ChromeX64", "")
	x64.Name = "Chrome"
	return map[int]map[string]catalog.Item{
		1: {
			"Chrome":   arm,
			"ArmOnly":  pkg("ArmOnly", "is false"),
			"NeedsArm": pkg("NeedsArm", "", "ArmOnly"),
			"Plain":    pkg("Plain", ""),
			"ArmPatch": pkg("ArmPatch", "is false"),
		},
		2: {"Chrome": x64},
	}
}

// A variant in a later catalog serves when the first one is not for this machine.
func TestResolveSkipsNotInstallableVariant(t *testing.T) {
	item, ok := firstItem("Chrome", conditionCatalogs())
	if !ok || item.DisplayName != "ChromeX64" {
		t.Fatalf("firstItem(Chrome) = %q, %v; want ChromeX64", item.DisplayName, ok)
	}
}

// An item is never installed where its condition fails, but stays removable:
// a machine that stops matching must still be able to remove it.
func TestNotInstallableIsRemovable(t *testing.T) {
	cats := conditionCatalogs()
	if _, ok := firstItem("ArmOnly", cats); ok {
		t.Error("firstItem returned an item whose installable_condition is false")
	}
	if _, err := ResolveItem("ArmOnly", cats); !errors.Is(err, ErrNotInstallable) {
		t.Errorf("ResolveItem error = %v, want ErrNotInstallable", err)
	}
	if item, ok := removalItem("ArmOnly", cats); !ok || item.DisplayName != "ArmOnly" {
		t.Errorf("removalItem = %q, %v; want ArmOnly", item.DisplayName, ok)
	}

	installerInstall = fakeUninstall
	actualUninstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualUninstalledItems = nil
	}()
	Uninstalls([]string{"ArmOnly"}, cats, &installer.Runner{Report: report.New()})
	if !reflect.DeepEqual(actualUninstalledItems, []string{"ArmOnly"}) {
		t.Errorf("uninstalled %v, want [ArmOnly]", actualUninstalledItems)
	}
}

// A not-installable item is skipped without a failure record, so a variant or
// an updater meant for other hardware does not fail every run; an item that
// depends on one cannot be installed and fails.
func TestInstallsNotInstallable(t *testing.T) {
	installerInstall = fakeInstall
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	r := &installer.Runner{Report: report.New()}
	Installs([]string{"ArmOnly", "ArmPatch", "NeedsArm", "Plain", "Chrome"}, conditionCatalogs(), r, nil)

	if want := []string{"Plain", "ChromeX64"}; !reflect.DeepEqual(actualInstalledItems, want) {
		t.Errorf("installed %v, want %v", actualInstalledItems, want)
	}
	assertFailedItems(t, r, map[string]string{
		"NeedsArm": "dependency ArmOnly is not installable on this machine",
	})
}

// Manifests keeps not-installable names so the run can say why it skipped them.
func TestManifestsKeepsNotInstallable(t *testing.T) {
	installs, _, updates := Manifests([]manifest.Item{{Installs: []string{"ArmOnly", "Missing"}, Updates: []string{"ArmPatch"}}}, conditionCatalogs())
	if !reflect.DeepEqual(installs, []string{"ArmOnly"}) || !reflect.DeepEqual(updates, []string{"ArmPatch"}) {
		t.Errorf("installs %v, updates %v; want [ArmOnly], [ArmPatch]", installs, updates)
	}
}
