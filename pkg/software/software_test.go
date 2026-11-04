package software

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// useReport points MunkiReport at path for the test.
func useReport(t *testing.T, path string) {
	t.Helper()
	orig := MunkiReport
	MunkiReport = path
	t.Cleanup(func() { MunkiReport = orig })
}

func writeReport(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ManagedInstallReport.plist")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMunkiReport: only InstalledItems counts. An item Munki manages but has
// not installed, or one it removed, appears elsewhere in the report and must
// not count as present.
func TestMunkiReport(t *testing.T) {
	useReport(t, filepath.Join("testdata", "ManagedInstallReport.plist"))
	for name, want := range map[string]bool{
		"DemoApp":         true,
		"Example Suite":   true,
		"dialog":          true,
		"MissingTool":     false, // in ManagedInstalls and managed_installs_list only
		"RemovedApp":      false, // in RemovedItems
		"production":      false, // a string value elsewhere
		"demoapp":         false, // names are case-sensitive, as in Munki
		"InstalledItems":  false,
		"NotInTheReport":  false,
		"munkitools_core": true,
	} {
		if got := munkiReportLists(name); got != want {
			t.Errorf("munkiReportLists(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestMunkiReportUnusable: a report sofcat cannot read lists nothing, so the
// software counts as missing and the item waits instead of installing.
func TestMunkiReportUnusable(t *testing.T) {
	cases := map[string]string{
		"missing":           filepath.Join(t.TempDir(), "absent.plist"),
		"no InstalledItems": filepath.Join("testdata", "NoInstalledItems.plist"),
		"garbage":           writeReport(t, "not a plist <dict"),
		"binary":            writeReport(t, "bplist00\x00\x01<plist><dict><key>InstalledItems</key><array><string>DemoApp</string></array></dict></plist>"),
		"directory":         t.TempDir(),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			useReport(t, path)
			if munkiReportLists("DemoApp") {
				t.Error("DemoApp counted as present")
			}
		})
	}
}

func TestChocolateyLib(t *testing.T) {
	lib := t.TempDir()
	orig := ChocolateyLib
	ChocolateyLib = func() string { return lib }
	t.Cleanup(func() { ChocolateyLib = orig })
	if err := os.Mkdir(filepath.Join(lib, "demo-tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "a-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{
		"demo-tool": true,
		"other":     false,
		"a-file":    false, // a package is a directory
		"..":        false,
		"../lib":    false,
		"":          false,
	} {
		if got := chocolateyLibHas(id); got != want {
			t.Errorf("chocolateyLibHas(%q) = %v, want %v", id, got, want)
		}
	}
}

// TestMissing: each OS checks only its own key, an entry without one is
// skipped, and missing names keep catalog order.
func TestMissing(t *testing.T) {
	origOS, origMunki, origChoco := GOOS, munkiInstalled, chocolateyInstalled
	t.Cleanup(func() { GOOS, munkiInstalled, chocolateyInstalled = origOS, origMunki, origChoco })
	present := map[string]bool{"have-munki": true, "have-choco": true}
	munkiInstalled = func(name string) bool { return present[name] }
	chocolateyInstalled = func(id string) bool { return present[id] }

	item := catalog.Item{RequiresSoftware: []catalog.RequiredSoftware{
		{DisplayName: "Zeta", Munki: "gone", Chocolatey: "gone"},
		{DisplayName: "Both Present", Munki: "have-munki", Chocolatey: "have-choco"},
		{DisplayName: "Mac Only", Munki: "gone"},
		{DisplayName: "Windows Only", Chocolatey: "gone"},
		{DisplayName: "Alpha", Munki: "gone", Chocolatey: "gone"},
	}}
	for goos, want := range map[string][]string{
		"darwin":  {"Zeta", "Mac Only", "Alpha"},
		"windows": {"Zeta", "Windows Only", "Alpha"},
		"linux":   nil,
	} {
		GOOS = goos
		if got := Missing(item); !slices.Equal(got, want) {
			t.Errorf("%s: Missing = %q, want %q", goos, got, want)
		}
	}
	if got := Missing(catalog.Item{}); got != nil {
		t.Errorf("no requirements: Missing = %q", got)
	}
}
