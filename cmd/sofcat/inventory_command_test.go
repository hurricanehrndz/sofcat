//go:build !zip_only

// Plan building with command-installer fixtures, which a zip-only build
// rejects (process.ZipOnlyReason).

package main

import (
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// TestPlanBuilderKinds: the kind decides how osquery reads an item's status
// (a removal reads "removed", not "pending"), so the first kind a name gets
// must stick, and only items the install walk visits pull in their updaters.
// Versions must come from the catalog item the run acts on, and a removal the
// run cannot resolve is a warning, not a forever-pending item.
func TestPlanBuilderKinds(t *testing.T) {
	valid := catalog.InstallerItem{Type: "ps1", Location: "packages/x.ps1"}
	b := newPlanBuilder()
	b.catalogs = map[int]map[string]catalog.Item{
		1: {"App": {Name: "App", DisplayName: "Invalid Entry", Version: "0.9"}},
		2: {
			"App":       {Name: "App", DisplayName: "The App", Version: "1.0", Installer: valid},
			"Tool":      {Name: "Tool", Installer: valid},
			"AppPatch":  {Name: "AppPatch", Installer: valid},
			"OldPatch":  {Name: "OldPatch", Installer: valid},
			"ToolPatch": {Name: "ToolPatch", Installer: valid},
		},
	}
	b.add(report.KindManagedInstall, false, "App")
	b.add(report.KindManagedUninstall, false, "Old", "App")
	b.add(report.KindManagedUpdate, false, "Tool")
	b.addUpdaters(map[string][]string{
		"App":  {"AppPatch"},
		"Old":  {"OldPatch"},
		"Tool": {"ToolPatch"},
	})

	got := make(map[string]report.PlanItem)
	for _, pi := range b.items {
		got[pi.Name] = pi
	}
	if app := got["App"]; app.Kind != report.KindManagedInstall || app.DisplayName != "The App" || app.Version != "1.0" {
		t.Errorf("App = %#v, want first kind and the valid catalog entry", app)
	}
	if got["AppPatch"].Kind != report.KindUpdateFor {
		t.Errorf("AppPatch = %#v, want update_for", got["AppPatch"])
	}
	for _, name := range []string{"Old", "OldPatch", "ToolPatch"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s planned, want it left out", name)
		}
	}
	if len(b.warnings) != 1 || !strings.Contains(b.warnings[0], "Old") {
		t.Errorf("expected one warning for the unresolvable removal, got %v", b.warnings)
	}
}

// TestPlanBuilderNotInstallable: an item this machine may not install is
// skipped by the run, so the inventory must say why rather than report it
// pending forever. An offered optional install nobody chose is just not
// offered here and is not worth a warning; a removal is unaffected.
func TestPlanBuilderNotInstallable(t *testing.T) {
	valid := catalog.InstallerItem{Type: "ps1", Location: "packages/x.ps1"}
	b := newPlanBuilder()
	b.catalogs = map[int]map[string]catalog.Item{1: {
		"ArmApp": {Name: "ArmApp", Installer: valid, NotInstallable: "is false"},
		"ArmOpt": {Name: "ArmOpt", Installer: valid, NotInstallable: "is false"},
		"ArmOld": {Name: "ArmOld", Installer: valid, NotInstallable: "is false"},
	}}
	b.add(report.KindManagedInstall, false, "ArmApp")
	b.add(report.KindOptionalInstall, false, "ArmOpt")
	b.add(report.KindManagedUninstall, false, "ArmOld")

	if len(b.items) != 1 || b.items[0].Name != "ArmOld" {
		t.Errorf("planned %#v, want only the removal ArmOld", b.items)
	}
	if len(b.warnings) != 1 || !strings.Contains(b.warnings[0], "ArmApp") || !strings.Contains(b.warnings[0], "not installable") {
		t.Errorf("warnings = %v, want one for ArmApp", b.warnings)
	}
}

// TestFinishInventoryRemovesLegacyReport: upgraded machines carry a stale
// GorillaReport.json with the inherited, user-readable ACL; a real run must
