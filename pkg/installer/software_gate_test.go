package installer

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/software"
)

// stubMissingSoftware makes every item lack names until the test ends.
func stubMissingSoftware(t *testing.T, names ...string) {
	t.Helper()
	orig := missingSoftware
	missingSoftware = func(catalog.Item, software.Checks) []string { return names }
	t.Cleanup(func() { missingSoftware = orig })
}

// stubInstallers fails the test if anything runs for the item: a script, the
// installer or the uninstaller.
func stubInstallers(t *testing.T, allowUninstall bool) {
	t.Helper()
	origScript := itemScriptFunc
	statusCheckStatus = fakeCheckStatus
	installItemFunc = func(*Runner, catalog.Item, string) (string, error) {
		t.Error("installer ran while required software is missing")
		return "", nil
	}
	uninstallItemFunc = func(*Runner, catalog.Item, string) (string, error) {
		if !allowUninstall {
			t.Error("uninstaller ran")
		}
		return "", nil
	}
	itemScriptFunc = func(_, kind, _ string) error {
		if kind == "preinstall" || kind == "postinstall" {
			t.Errorf("%s script ran while required software is missing", kind)
		}
		return nil
	}
	t.Cleanup(func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		uninstallItemFunc = origUninstallItemFunc
		itemScriptFunc = origScript
	})
}

func softwareItem() catalog.Item {
	item := msiItem
	item.Name = "NeedsTools"
	item.DisplayName = statusActionNoError
	item.PreScript = "Write-Host pre"
	item.DependsCheck = []catalog.DependsCheck{{DisplayName: "Demo Tool", Munki: "DemoTool"}}
	return item
}

// TestSoftwareGateDefers: an install or update whose required software is
// missing runs nothing, and the deferral carries the code and the message the
// UI shows. Several names share one sentence.
func TestSoftwareGateDefers(t *testing.T) {
	for _, tc := range []struct {
		action  string
		missing []string
		want    string
	}{
		{"install", []string{"Demo Tool"}, "Needs Demo Tool. Install it first, then try again."},
		{"update", []string{"Demo Tool", "Other Tool"}, "Needs Demo Tool, Other Tool. Install it first, then try again."},
	} {
		t.Run(tc.action, func(t *testing.T) {
			stubInstallers(t, false)
			stubMissingSoftware(t, tc.missing...)
			r := newTestRunner()
			_, err := r.Install(softwareItem(), tc.action)
			if !errors.Is(err, ErrMissingSoftware) || !errors.Is(err, ErrDeferred) {
				t.Fatalf("err = %v, want ErrMissingSoftware wrapping ErrDeferred", err)
			}
			want := report.DeferredItem{Name: "NeedsTools", Version: "1.2.3", Action: tc.action, Reason: tc.want, Code: report.CodeMissingSoftware}
			if len(r.Report.DeferredItems) != 1 || r.Report.DeferredItems[0] != want {
				t.Errorf("deferred = %#v, want %#v", r.Report.DeferredItems, want)
			}
			if len(r.Report.FailedItems)+len(r.Report.InstalledItems) != 0 {
				t.Errorf("failed %v, installed %v", r.Report.FailedItems, r.Report.InstalledItems)
			}
		})
	}
}

// TestSoftwareGateCheckOnly: a check-only run reports the deferral instead of
// claiming it would install the item.
func TestSoftwareGateCheckOnly(t *testing.T) {
	stubInstallers(t, false)
	stubMissingSoftware(t, "Demo Tool")
	r := newTestRunner()
	r.CheckOnly = true
	if _, err := r.Install(softwareItem(), "install"); !errors.Is(err, ErrMissingSoftware) {
		t.Fatalf("err = %v, want ErrMissingSoftware", err)
	}
	if len(r.Report.DeferredItems) != 1 || r.Report.DeferredItems[0].Code != report.CodeMissingSoftware {
		t.Errorf("deferred = %#v", r.Report.DeferredItems)
	}
	if len(r.Report.InstalledItems) != 0 {
		t.Errorf("check-only listed the item as one it would install: %v", r.Report.InstalledItems)
	}
}

// TestSoftwareGateNotForUninstall: removing an item never waits for software
// it needed to install.
func TestSoftwareGateNotForUninstall(t *testing.T) {
	stubInstallers(t, true)
	stubMissingSoftware(t, "Demo Tool")
	r := newTestRunner()
	if _, err := r.Install(softwareItem(), "uninstall"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(r.Report.DeferredItems) != 0 {
		t.Errorf("uninstall deferred: %#v", r.Report.DeferredItems)
	}
}

// TestSoftwareGateOnlyWhenActionNeeded: an item already installed is not
// deferred because its required software went away.
func TestSoftwareGateOnlyWhenActionNeeded(t *testing.T) {
	stubInstallers(t, false)
	stubMissingSoftware(t, "Demo Tool")
	item := softwareItem()
	item.DisplayName = statusNoActionNoError
	r := newTestRunner()
	if _, err := r.Install(item, "install"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(r.Report.DeferredItems) != 0 {
		t.Errorf("installed item deferred: %#v", r.Report.DeferredItems)
	}
}

// TestSoftwareGateRunsChecks: the gate hands the entry's script for this OS
// to the script runner, with the run's cache path, and its sofcat item name
// to ItemInstalled. Either passing lets the item through; neither defers it.
func TestSoftwareGateRunsChecks(t *testing.T) {
	origFunc, origGOOS := dependsScriptFunc, software.GOOS
	t.Cleanup(func() { dependsScriptFunc, software.GOOS = origFunc, origGOOS })
	software.GOOS = "windows"

	item := softwareItem()
	item.DependsCheck = []catalog.DependsCheck{{
		DisplayName: "Demo Tool",
		Sofcat:      "demo-tool",
		Script:      map[string]string{"windows": "exit 1", "darwin": "#!/bin/sh\nexit 1"},
	}}
	for _, tc := range []struct {
		name              string
		installed, script bool
		deferred          bool
	}{
		{"neither", false, false, true},
		{"sofcat item installed", true, false, false},
		{"script passes", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dependsScriptFunc = func(script, cachePath string) error {
				if script != "exit 1" || cachePath != "testdata/cache" {
					t.Errorf("script runner got %q in %q", script, cachePath)
				}
				if tc.script {
					return nil
				}
				return errors.New("exit status 1")
			}
			r := newTestRunner()
			r.CachePath = "testdata/cache"
			r.ItemInstalled = func(name string) bool {
				if name != "demo-tool" {
					t.Errorf("ItemInstalled(%q), want demo-tool", name)
				}
				return tc.installed
			}
			err := r.softwareGate(item, "install")
			if tc.deferred != errors.Is(err, ErrMissingSoftware) {
				t.Errorf("softwareGate = %v, want deferred %v", err, tc.deferred)
			}
			if tc.deferred && (len(r.Report.DeferredItems) != 1 ||
				r.Report.DeferredItems[0].Reason != "Needs Demo Tool. Install it first, then try again.") {
				t.Errorf("deferred = %#v", r.Report.DeferredItems)
			}
		})
	}
}

// TestRunDependsScript: off Windows a depends_check script runs as an
// executable through its #! line, its exit status decides, and the temporary
// file is removed either way.
func TestRunDependsScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs the script through its #! line")
	}
	cache := t.TempDir()
	if err := runDependsScript("#!/bin/sh\nexit 0\n", cache); err != nil {
		t.Errorf("passing script: %v", err)
	}
	err := runDependsScript("#!/bin/sh\necho no demo-tool >&2\nexit 3\n", cache)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "no demo-tool") {
		t.Errorf("failing script: err = %v, want its exit status and stderr", err)
	}
	if left, _ := os.ReadDir(cache); len(left) != 0 {
		t.Errorf("temporary scripts left behind: %v", left)
	}
}
