package process

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// setZipOnly forces the zip-only rule set for one test.
func setZipOnly(t *testing.T, on bool) {
	t.Helper()
	saved := zipOnly
	zipOnly = on
	t.Cleanup(func() { zipOnly = saved })
}

func zipItem() catalog.Item {
	return catalog.Item{Installer: catalog.InstallerItem{
		Type:     "zip",
		Variants: map[string]map[string]catalog.Variant{"claude-skills": {"windows": {Location: "a.zip"}}},
	}}
}

// A zip-only binary has no command installer and runs no catalog script
// (spec R21), so every item that needs one must be rejected with a reason the
// admin can act on, while a plain zip item stays valid.
func TestZipOnlyReason(t *testing.T) {
	setZipOnly(t, true)
	with := func(edit func(*catalog.Item)) catalog.Item {
		item := zipItem()
		edit(&item)
		return item
	}
	for name, tc := range map[string]struct {
		item catalog.Item
		want string
	}{
		"exe installer":        {catalog.Item{Installer: catalog.InstallerItem{Type: "exe", Location: "a.exe"}}, `installer.type "exe" is not zip`},
		"msi uninstaller":      {with(func(i *catalog.Item) { i.Uninstaller.Type = "msi" }), `uninstaller.type "msi" is not zip`},
		"preinstall_script":    {with(func(i *catalog.Item) { i.PreScript = "x" }), "sets preinstall_script"},
		"postinstall_script":   {with(func(i *catalog.Item) { i.PostScript = "x" }), "sets postinstall_script"},
		"preuninstall_script":  {with(func(i *catalog.Item) { i.PreUninstallScript = "x" }), "sets preuninstall_script"},
		"postuninstall_script": {with(func(i *catalog.Item) { i.PostUninstallScript = "x" }), "sets postuninstall_script"},
		"check.script":         {with(func(i *catalog.Item) { i.Check.Script = "x" }), "sets check.script"},
	} {
		if got := ZipOnlyReason(tc.item); !strings.Contains(got, tc.want) {
			t.Errorf("%s: reason %q, want it to contain %q", name, got, tc.want)
		}
	}

	if got := ZipOnlyReason(zipItem()); got != "" {
		t.Errorf("zip item rejected: %q", got)
	}
	if got := ZipOnlyReason(with(func(i *catalog.Item) { i.Uninstaller.Type = "zip" })); got != "" {
		t.Errorf("zip item with a zip uninstaller rejected: %q", got)
	}
}

// The stock build applies none of the zip-only rules.
func TestZipOnlyReasonStock(t *testing.T) {
	setZipOnly(t, false)
	item := catalog.Item{Installer: catalog.InstallerItem{Type: "exe"}, PostScript: "x"}
	if got := ZipOnlyReason(item); got != "" {
		t.Errorf("stock build rejected an exe item: %q", got)
	}
}

// A rejected item is neither installed nor removed by the run, and a later
// catalog's valid occurrence still wins.
func TestResolveZipOnly(t *testing.T) {
	setZipOnly(t, true)
	scripted := zipItem()
	scripted.PostScript = "x"
	exe := catalog.Item{Installer: catalog.InstallerItem{Type: "exe", Location: "a.exe"}}
	catalogs := map[int]map[string]catalog.Item{
		1: {"Scripted": scripted, "Exe": exe, "Later": exe},
		2: {"Later": zipItem()},
	}
	for _, name := range []string{"Scripted", "Exe"} {
		if _, err := ResolveItem(name, catalogs); !errors.Is(err, ErrZipOnly) {
			t.Errorf("ResolveItem(%s) err = %v, want ErrZipOnly", name, err)
		}
		if _, ok := removalItem(name, catalogs); ok {
			t.Errorf("removalItem(%s) resolved a rejected item", name)
		}
	}
	if item, err := ResolveItem("Later", catalogs); err != nil || item.Installer.Type != "zip" {
		t.Errorf("ResolveItem(Later) = %+v, %v; want the zip occurrence", item.Installer, err)
	}
}

// The service resolves items on every run and list call; the reason is
// logged at warn once, not on each.
func TestWarnZipOnlyOnce(t *testing.T) {
	var buf bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(saved) })

	WarnZipOnly("WarnOnce", "sets check.script")
	WarnZipOnly("WarnOnce", "sets check.script")
	if got := strings.Count(buf.String(), "level=WARN"); got != 1 || !strings.Contains(buf.String(), "sets check.script") {
		t.Errorf("logged %d warnings, want 1 with the reason:\n%s", got, buf.String())
	}
}

// A rejected dependency is skipped like a not-installable one: the build can
// never install it, so it is not a failure every run, and its dependent
// cannot be installed and fails.
func TestInstallsZipOnlyRejectedDependency(t *testing.T) {
	setZipOnly(t, true)
	installerInstall = fakeInstall
	actualInstalledItems = nil
	t.Cleanup(func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	})
	needsExe := zipItem()
	needsExe.Name, needsExe.DisplayName = "NeedsExe", "NeedsExe"
	needsExe.Dependencies = []string{"Exe"}
	catalogs := map[int]map[string]catalog.Item{1: {
		"Exe":      {Name: "Exe", DisplayName: "Exe", Installer: catalog.InstallerItem{Type: "exe", Location: "a.exe"}},
		"NeedsExe": needsExe,
	}}

	r := &installer.Runner{Report: report.New()}
	Installs([]string{"NeedsExe", "Exe"}, catalogs, r, nil)

	if len(actualInstalledItems) != 0 {
		t.Errorf("installed %v, want nothing", actualInstalledItems)
	}
	assertFailedItems(t, r, map[string]string{
		"NeedsExe": "dependency Exe is rejected by this zip-only build",
	})
}
