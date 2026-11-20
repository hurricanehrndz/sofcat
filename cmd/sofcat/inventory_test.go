package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// replace it with inventory.json and delete it.
func TestFinishInventoryRemovesLegacyReport(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacyReportFile)
	if err := os.WriteFile(legacy, []byte(`{"stale":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	finishInventory(config.Configuration{AppDataPath: dir}, report.New(), newPlanBuilder(), time.Now(), nil)

	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("legacy report still present after the run: %v", err)
	}
	readInventory(t, dir)
}

// TestFinishInventoryCheckOnlyLeavesInstalledRecord: readers take
// inventory.json as what is installed, so a check-only run, which installs
// nothing, must leave it byte for byte and keep the legacy report. It records
// itself in inventory-checkonly.json instead and still prints the inventory
// as the only stdout, which wrappers parse.
func TestFinishInventoryCheckOnlyLeavesInstalledRecord(t *testing.T) {
	dir := t.TempDir()
	installed := []byte(`{"from":"a real run"}`)
	if err := os.WriteFile(filepath.Join(dir, inventoryFile), installed, 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, legacyReportFile)
	if err := os.WriteFile(legacy, []byte(`{"stale":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout := captureStdout(t, func() {
		finishInventory(config.Configuration{AppDataPath: dir, CheckOnly: true}, report.New(), newPlanBuilder(), time.Now(), nil)
	})

	if err := json.Unmarshal([]byte(stdout), &report.Inventory{}); err != nil {
		t.Fatalf("stdout is not the inventory JSON: %v (%q)", err, stdout)
	}
	saved, err := os.ReadFile(filepath.Join(dir, checkOnlyInventoryFile))
	if err != nil {
		t.Fatalf("read check-only inventory: %v", err)
	}
	if strings.TrimSpace(stdout) != strings.TrimSpace(string(saved)) {
		t.Errorf("check-only record differs from the printed inventory:\n%s\nvs\n%s", saved, stdout)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, inventoryFile)); !bytes.Equal(got, installed) {
		t.Errorf("check-only changed inventory.json: %q", got)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("check-only removed the legacy report: %v", err)
	}
}

// TestFinishInventoryRealRunWritesInstalled: a real run's inventory is
// inventory.json and nothing else; it never writes a check-only record.
func TestFinishInventoryRealRunWritesInstalled(t *testing.T) {
	dir := t.TempDir()

	finishInventory(config.Configuration{AppDataPath: dir}, report.New(), newPlanBuilder(), time.Now(), nil)

	readInventory(t, dir)
	if _, err := os.Stat(filepath.Join(dir, checkOnlyInventoryFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("real run wrote %s: %v", checkOnlyInventoryFile, err)
	}
}

// TestPlanBuilderRequestedBy: the inventory names who asked for the
// self-service item a run carries out, and only that: an admin item of the
// same name is not a self-service request.
func TestPlanBuilderRequestedBy(t *testing.T) {
	b := newPlanBuilder()
	b.requestedBy = map[string]string{"Chrome": `PC\alice`, "Admin": `PC\alice`}
	b.add(report.KindManagedInstall, false, "Admin")
	b.add(report.KindOptionalInstall, true, "Chrome", "Other")

	inv := report.New().Inventory(report.Plan{Items: b.items})
	got := make(map[string]string)
	for _, it := range inv.ManagedInstalls {
		got[it.Name] = it.RequestedBy
	}
	want := map[string]string{"Admin": "", "Chrome": `PC\alice`, "Other": ""}
	for name, who := range want {
		if got[name] != who {
			t.Errorf("%s requested_by = %q, want %q", name, got[name], who)
		}
	}
}
