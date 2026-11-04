package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
