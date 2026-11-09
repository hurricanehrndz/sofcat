//go:build !zip_only

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
	"github.com/hurricanehrndz/sofcat/pkg/software"
)

// TestManagedRunReconcilesSelfServe verifies the run loads, reconciles, and
// saves the self-serve manifest — asserting a server default once (R4) even in
// check-only, and authorizing installs against the admin optional list (R2).
func TestManagedRunReconcilesSelfServe(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	// Deferred, not t.Cleanup: cleanups run after this function returns, so the
	// log handle would still be open when t.TempDir removes its directory. That
	// is fine on POSIX but fails on Windows, where an open file cannot be
	// unlinked. Defers run before cleanups, so the log closes first.
	defer sofcatlog.Close()

	const manifestYAML = `name: wiretest_manifest
default_installs:
  - DemoDefault
optional_installs:
  - DemoDefault
  - DemoOptional
catalogs:
  - wiretest_catalog
`
	const catalogYAML = `DemoDefault:
  display_name: Demo Default
  check:
    file:
      - path: C:\does-not-exist\demodefault.txt
  installer:
    type: ps1
    location: packages/demodefault.ps1
    hash: deadbeef
DemoOptional:
  display_name: Demo Optional
  installer:
    type: ps1
    location: packages/demooptional.ps1
    hash: deadbeef
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/wiretest_manifest.yaml"):
			_, _ = w.Write([]byte(manifestYAML))
		case strings.HasSuffix(r.URL.Path, "/catalogs/wiretest_catalog.yaml"):
			_, _ = w.Write([]byte(catalogYAML))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	appData := t.TempDir()
	cfg := config.Configuration{
		CheckOnly:   true,
		CachePath:   t.TempDir(),
		AppDataPath: appData,
		URL:         srv.URL + "/",
		URLPackages: srv.URL + "/",
		Manifest:    "wiretest_manifest",
		Catalogs:    []string{"wiretest_catalog"},
	}

	adminCheckFunc = func() (bool, error) { return true, nil }
	mkdirAllFunc = func(string, os.FileMode) error { return nil }

	if _, err := managedRun(cfg, nil, nil); err != nil {
		t.Fatalf("managedRun failed: %v", err)
	}

	// The default must be asserted into the self-serve file even in check-only.
	entry, err := manifest.LoadSelfServe(manifest.SelfServePath(appData))
	if err != nil {
		t.Fatalf("load self-serve: %v", err)
	}
	if !contains(entry.Installs, "DemoDefault") {
		t.Errorf("expected DemoDefault in managed_installs, got %#v", entry.Installs)
	}
	if !contains(entry.DefaultInstalls, "DemoDefault") {
		t.Errorf("expected DemoDefault in default_installs record, got %#v", entry.DefaultInstalls)
	}

	// A real run attaches the supplied callback to Runner.Emit. The fixture's
	// deliberately invalid package hash still emits downloading/failed records,
	// which is enough to prove the run-scoped seam without changing sequencing.
	cfg.CheckOnly = false
	var states []string
	if _, err := managedRun(cfg, func(_ catalog.Item, _ []string, state string, _ int, _ string) { states = append(states, state) }, nil); err != nil {
		t.Fatalf("managedRun with progress callback failed: %v", err)
	}
	if len(states) == 0 || states[0] != "downloading" {
		t.Fatalf("progress callback did not reach Runner.Emit: %v", states)
	}

	// The real run's inventory covers the default it tried (and failed, on the
	// bad hash) and the optional the user never selected.
	inv := readInventory(t, appData)
	if inv.ManifestName != "wiretest_manifest" {
		t.Errorf("ManifestName = %q", inv.ManifestName)
	}
	byName := make(map[string]report.InventoryItem)
	for _, it := range inv.ManagedInstalls {
		byName[it.Name] = it
	}
	if it := byName["DemoDefault"]; it.Kind != report.KindDefaultInstall || it.Status != report.StatusFailed || !it.SelfService || it.DisplayName != "Demo Default" {
		t.Errorf("DemoDefault = %#v", it)
	}
	if it := byName["DemoOptional"]; it.Kind != report.KindOptionalInstall || it.Status != report.StatusAvailable || it.SelfService {
		t.Errorf("DemoOptional = %#v", it)
	}
}

// TestManagedRunHoldsUnknownConditionalItems verifies a conditional_items
// block whose condition names a fact this machine lacks is held, neither
// planned nor dropped silently: it reaches the report and the inventory's
// warnings, while a true block's items join the plan as usual.
func TestManagedRunHoldsUnknownConditionalItems(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	// Deferred so the log closes before t.TempDir cleanup (see above).
	defer sofcatlog.Close()

	manifestYAML := `name: wiretest_manifest
catalogs:
  - wiretest_catalog
conditional_items:
  - condition: '"lab" in machine_groups'
    managed_installs:
      - LabTool
  - condition: 'os == "` + runtime.GOOS + `"'
    managed_installs:
      - OSTool
`
	const catalogYAML = `LabTool:
  display_name: Lab Tool
  installer:
    type: ps1
    location: packages/labtool.ps1
    hash: deadbeef
OSTool:
  display_name: OS Tool
  installer:
    type: ps1
    location: packages/ostool.ps1
    hash: deadbeef
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/wiretest_manifest.yaml"):
			_, _ = w.Write([]byte(manifestYAML))
		case strings.HasSuffix(r.URL.Path, "/catalogs/wiretest_catalog.yaml"):
			_, _ = w.Write([]byte(catalogYAML))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := config.Configuration{
		CheckOnly:   true,
		CachePath:   t.TempDir(),
		AppDataPath: t.TempDir(),
		URL:         srv.URL + "/",
		URLPackages: srv.URL + "/",
		Manifest:    "wiretest_manifest",
		Catalogs:    []string{"wiretest_catalog"},
	}

	adminCheckFunc = func() (bool, error) { return true, nil }
	mkdirAllFunc = func(string, os.FileMode) error { return nil }

	// A check-only run prints its inventory rather than saving it.
	var run *report.Report
	stdout := captureStdout(t, func() {
		var err error
		if run, err = managedRun(cfg, nil, nil); err != nil {
			t.Fatalf("managedRun failed: %v", err)
		}
	})
	var inv report.Inventory
	if err := json.Unmarshal([]byte(stdout), &inv); err != nil {
		t.Fatalf("decode inventory: %v\n%s", err, stdout)
	}

	if len(run.HeldConditions) != 1 {
		t.Fatalf("HeldConditions = %#v, want the lab block only", run.HeldConditions)
	}
	h := run.HeldConditions[0]
	if h.Condition != `"lab" in machine_groups` || h.Reason != "facts unavailable" ||
		!slices.Equal(h.Missing, []string{"machine_groups"}) || !slices.Equal(h.Items, []string{"LabTool"}) {
		t.Errorf("held = %#v", h)
	}

	var names []string
	for _, it := range inv.ManagedInstalls {
		names = append(names, it.Name)
	}
	if contains(names, "LabTool") || !contains(names, "OSTool") {
		t.Errorf("ManagedInstalls = %v, want OSTool without LabTool", names)
	}
	want := `conditional_items "\"lab\" in machine_groups" held: facts unavailable (machine_groups); items: LabTool`
	if !contains(inv.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", inv.Warnings, want)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestRouteCheckOnlyPrintsInventoryOnly: -C prints the inventory JSON to
// stdout and tools pipe it to jq, so a deferral must not add the run summary.
func TestRouteCheckOnlyPrintsInventoryOnly(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	// Deferred so the log closes before t.TempDir cleanup (see above).
	defer sofcatlog.Close()
	origOS, origLib := software.GOOS, software.ChocolateyLib
	lib := t.TempDir()
	software.GOOS = "windows"
	software.ChocolateyLib = func() string { return lib }
	t.Cleanup(func() { software.GOOS, software.ChocolateyLib = origOS, origLib })

	const manifestYAML = `name: wiretest_manifest
catalogs:
  - wiretest_catalog
managed_installs:
  - NeedsTool
`
	const catalogYAML = `NeedsTool:
  display_name: Needs Tool
  check:
    file:
      - path: C:\does-not-exist\needstool.txt
  requires_software:
    - display_name: Demo Tool
      chocolatey: demo-tool
  installer:
    type: ps1
    location: packages/needstool.ps1
    hash: deadbeef
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/wiretest_manifest.yaml"):
			_, _ = w.Write([]byte(manifestYAML))
		case strings.HasSuffix(r.URL.Path, "/catalogs/wiretest_catalog.yaml"):
			_, _ = w.Write([]byte(catalogYAML))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := config.Configuration{
		CheckOnly:   true,
		CachePath:   t.TempDir(),
		AppDataPath: t.TempDir(),
		URL:         srv.URL + "/",
		URLPackages: srv.URL + "/",
		Manifest:    "wiretest_manifest",
		Catalogs:    []string{"wiretest_catalog"},
	}
	adminCheckFunc = func() (bool, error) { return true, nil }
	mkdirAllFunc = func(string, os.FileMode) error { return nil }
	var run *report.Report
	managedRunFunc = func(cfg config.Configuration, p installer.ProgressFn, c *installer.Cancels) (*report.Report, error) {
		var err error
		run, err = managedRun(cfg, p, c)
		return run, err
	}

	stdout := captureStdout(t, func() {
		if err := route(cfg); err != nil {
			t.Fatalf("route: %v", err)
		}
	})
	if len(run.DeferredItems) != 1 || run.DeferredItems[0].Code != report.CodeMissingSoftware {
		t.Fatalf("DeferredItems = %+v, want NeedsTool deferred for missing software", run.DeferredItems)
	}
	// The whole output is one JSON document: no summary line before or after.
	var inv report.Inventory
	if err := json.Unmarshal([]byte(stdout), &inv); err != nil {
		t.Fatalf("check-only stdout is not JSON only: %v\n%s", err, stdout)
	}
}

// TestUserPlanSetsHoldWithdrawnItems verifies an item the user canceled
// mid-run is held in the user plan, not desired: the cancel reverted the
// selection after desired was computed, and a saved plan reaches signed-in
// users at once, so the helper must neither place nor remove it.
func TestUserPlanSetsHoldWithdrawnItems(t *testing.T) {
	desired := map[string]bool{"Kept": true, "Canceled": true, "Deferred": true}
	held := map[string]bool{"Held": true}
	deferred := []report.DeferredItem{{Name: "Deferred"}}

	planDesired, planHeld := userPlanSets(desired, held, deferred, []string{"Canceled", "CanceledRemoval"})

	for _, name := range []string{"Canceled", "Deferred"} {
		if planDesired[name] {
			t.Errorf("%s is desired in the user plan", name)
		}
	}
	if !planDesired["Kept"] {
		t.Error("Kept is missing from the desired set")
	}
	// A canceled removal (never desired) is held too, so it is not removed.
	for _, name := range []string{"Held", "Canceled", "CanceledRemoval", "Deferred"} {
		if !planHeld[name] {
			t.Errorf("%s is not held in the user plan", name)
		}
	}
	if len(desired) != 3 || len(held) != 1 {
		t.Error("userPlanSets changed the run's own sets, which scope exit used")
	}
}
