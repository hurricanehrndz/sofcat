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

// TestMissing: an entry is present when any check that applies on this OS
// passes, an entry with no check for this OS is skipped, and missing names
// keep catalog order.
func TestMissing(t *testing.T) {
	origOS, origMunki := GOOS, munkiInstalled
	t.Cleanup(func() { GOOS, munkiInstalled = origOS, origMunki })
	munkiInstalled = func(name string) bool { return name == "have-munki" }
	checks := Checks{
		Sofcat: func(name string) bool { return name == "have-item" },
		Script: func(script string) bool { return script == "pass" },
	}
	scripts := func(result string) map[string]string {
		return map[string]string{"darwin": result, "windows": result}
	}

	item := catalog.Item{DependsCheck: []catalog.DependsCheck{
		{DisplayName: "Zeta", Sofcat: "gone", Munki: "gone", Script: scripts("fail")},
		{DisplayName: "By Sofcat", Sofcat: "have-item", Munki: "gone", Script: scripts("fail")},
		{DisplayName: "By Munki", Sofcat: "gone", Munki: "have-munki"},
		{DisplayName: "By Script", Sofcat: "gone", Script: scripts("pass")},
		{DisplayName: "Munki Only", Munki: "gone"},
		{DisplayName: "Windows Script Only", Script: map[string]string{"windows": "fail"}},
		{DisplayName: "Alpha", Sofcat: "gone"},
	}}
	for goos, want := range map[string][]string{
		// By Munki: its munki key does not apply off a Mac, so only sofcat does.
		"darwin":  {"Zeta", "Munki Only", "Alpha"},
		"windows": {"Zeta", "By Munki", "Windows Script Only", "Alpha"},
		"linux":   {"Zeta", "By Munki", "By Script", "Alpha"},
	} {
		GOOS = goos
		if got := Missing(item, checks); !slices.Equal(got, want) {
			t.Errorf("%s: Missing = %q, want %q", goos, got, want)
		}
	}

	// A check the caller does not provide never passes.
	GOOS = "windows"
	if got := Missing(item, Checks{}); !slices.Equal(got, []string{"Zeta", "By Sofcat", "By Munki", "By Script", "Windows Script Only", "Alpha"}) {
		t.Errorf("no checks: Missing = %q", got)
	}
	if got := Missing(catalog.Item{}, checks); got != nil {
		t.Errorf("no requirements: Missing = %q", got)
	}
}
