package service

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// saveStatusPlan stores a user plan listing each item/placement pair.
func saveStatusPlan(t *testing.T, appData string, pairs ...[2]string) {
	t.Helper()
	var entries []UserPlacement
	for _, p := range pairs {
		entries = append(entries, UserPlacement{Item: p[0], Placement: p[1]})
	}
	if err := SaveUserPlan(appData, UserPlacements{Entries: entries}); err != nil {
		t.Fatal(err)
	}
}

func recordStatus(t *testing.T, appData, sid string, at time.Time, results ...UserPlacementResult) {
	t.Helper()
	if err := recordUserPlacementStatus(appData, sid, results, at); err != nil {
		t.Fatal(err)
	}
}

// stubUserScopeList serves one zip item, "Skill", whose only placement,
// codex, is user scope, and stores a plan listing it.
func stubUserScopeList(t *testing.T, cfg config.Configuration) {
	t.Helper()
	const goos = placement.Windows
	table := placement.Table{
		Placements: map[string]placement.Spec{"codex": {
			Paths: map[string]string{goos: `${USERPROFILE}\.agents\skills`},
			Scope: map[string]string{goos: placement.ScopeUser},
		}},
		Order: []string{"codex"},
	}
	skill := catalog.Item{Name: "Skill", Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{"codex": {goos: {}}}}}
	stubZipList(t, map[int]map[string]catalog.Item{1: {"Skill": skill}}, func(config.Configuration) (placement.Table, error) { return table, nil })
	newZipInstaller = func(cfg config.Configuration, table placement.Table) *installer.ZipInstaller {
		z := installer.NewMachineZip(cfg, table)
		z.Guard = placement.Guard{GOOS: goos, DataDir: cfg.AppDataPath}
		return z
	}
	saveStatusPlan(t, cfg.AppDataPath, [2]string{"Skill", "codex"})
}

// listStatus is Skill's status as getOptionalItems lists it for sid.
func listStatus(t *testing.T, cfg config.Configuration, sid string) string {
	t.Helper()
	items, err := getOptionalItems(cfg, peer{ID: sid})
	if err != nil || len(items) != 1 {
		t.Fatalf("getOptionalItems(%q) = %+v, %v", sid, items, err)
	}
	return items[0].Status
}

// TestListUserScopeStatus: the service's machine view has no user receipts,
// so a user-scope item the user agent placed read "Will be installed"
// forever. The card now follows what that user's agent last reported, and
// another user, who reported nothing, still gets the machine view.
func TestListUserScopeStatus(t *testing.T) {
	appData := t.TempDir()
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"production"}}
	stubUserScopeList(t, cfg)
	statusFor := func(sid string) string { t.Helper(); return listStatus(t, cfg, sid) }
	now := time.Now().UTC()
	recordStatus(t, appData, "S-A", now, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "installed"})
	if got := statusFor("S-A"); got != "Installed" {
		t.Errorf("A after installed = %s, want Installed", got)
	}
	for _, sid := range []string{"S-B", ""} {
		if got := statusFor(sid); got != "NotInstalled" {
			t.Errorf("%q, which reported nothing = %s, want the machine view's NotInstalled", sid, got)
		}
	}

	// A conflict says nothing new: the tree was placed and still is.
	recordStatus(t, appData, "S-A", now, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "conflict"})
	if got := statusFor("S-A"); got != "Installed" {
		t.Errorf("A after conflict = %s, want the prior Installed", got)
	}

	recordStatus(t, appData, "S-A", now, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "removed"})
	if got := statusFor("S-A"); got != "NotInstalled" {
		t.Errorf("A after removed = %s, want NotInstalled", got)
	}
}

// TestListUserScopeStatusFollowsPlan: a run that drops an item saves the
// plan before the agent reports the removal, if it ever does, so a stored
// "installed" the plan no longer lists must not keep the card Installed.
func TestListUserScopeStatusFollowsPlan(t *testing.T) {
	appData := t.TempDir()
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"production"}}
	stubUserScopeList(t, cfg)
	recordStatus(t, appData, "S-A", time.Now().UTC(), UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "installed"})
	saveStatusPlan(t, appData)
	if got := listStatus(t, cfg, "S-A"); got != "NotInstalled" {
		t.Errorf("after the plan dropped Skill = %s, want NotInstalled", got)
	}
}

// TestListUserScopeStatusOverThePipe: the storefront's list call reaches
// the status under the same caller the report was stored for.
func TestListUserScopeStatusOverThePipe(t *testing.T) {
	cfg := testServiceConfig(t)
	cfg.CachePath, cfg.Catalogs = t.TempDir(), []string{"production"}
	stubUserScopeList(t, cfg)
	startTestRunner(t, cfg, noopRun)
	client := NewClient(cfg.ServicePipeName)
	ctx := context.Background()
	if err := client.ReportUserPlacements(ctx, []UserPlacementResult{{Item: "Skill", Placement: "codex", Outcome: "installed"}}); err != nil {
		t.Fatal(err)
	}
	items, err := client.ListOptionalInstalls(ctx)
	if err != nil || len(items) != 1 || items[0].Status != "Installed" {
		t.Errorf("ListOptionalInstalls = %+v, %v; want Skill Installed", items, err)
	}
}

// TestRecordUserPlacementStatusMerge: a report merges into the user's
// entries, since held items are not reported and must keep theirs; results
// for what the plan does not list, and regenerate results with no item, are
// dropped, and entries the plan stopped listing are pruned.
func TestRecordUserPlacementStatusMerge(t *testing.T) {
	appData := t.TempDir()
	saveStatusPlan(t, appData, [2]string{"held", "codex"}, [2]string{"a", "codex"}, [2]string{"gone", "codex"})
	now := time.Now().UTC()
	recordStatus(t, appData, "S-A", now,
		UserPlacementResult{Item: "held", Placement: "codex", Outcome: "installed"},
		UserPlacementResult{Item: "gone", Placement: "codex", Outcome: "installed"})

	saveStatusPlan(t, appData, [2]string{"held", "codex"}, [2]string{"a", "codex"})
	recordStatus(t, appData, "S-A", now,
		UserPlacementResult{Item: "a", Placement: "codex", Outcome: "current"},
		UserPlacementResult{Item: "a", Placement: "other", Outcome: "installed"},
		UserPlacementResult{Item: "stranger", Placement: "codex", Outcome: "installed"},
		UserPlacementResult{Placement: "codex", Outcome: "installed"},
		UserPlacementResult{Item: "a", Placement: "codex", Outcome: "bogus"})
	recordStatus(t, appData, "", now, UserPlacementResult{Item: "a", Placement: "codex", Outcome: "installed"})

	userPlacementStatusMu.Lock()
	status := loadUserPlacementStatus(appData)
	userPlacementStatusMu.Unlock()
	want := map[string]map[string]string{"held": {"codex": "installed"}, "a": {"codex": "current"}}
	got := map[string]map[string]string{}
	for item, placements := range status["S-A"] {
		got[item] = map[string]string{}
		for name, o := range placements {
			got[item][name] = o.Outcome
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) || len(status) != 1 {
		t.Errorf("status = %v (users %d), want %v for S-A only", got, len(status), want)
	}
	if info, err := os.Stat(userPlacementStatusPath(appData)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Errorf("status file = %v, %v; want 0600", info, err)
	}
}

// TestRecordUserPlacementStatusBounds: the file keeps at most
// maxUserPlacementStatusUsers users, dropping the one heard from longest
// ago, and a corrupt file reads as empty rather than failing a report.
func TestRecordUserPlacementStatusBounds(t *testing.T) {
	appData := t.TempDir()
	saveStatusPlan(t, appData, [2]string{"a", "codex"})
	if err := os.WriteFile(userPlacementStatusPath(appData), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := userPlacementStatusFor(appData, "S-0"); got != nil {
		t.Errorf("corrupt file = %v, want empty", got)
	}
	start := time.Now().UTC()
	for i := range maxUserPlacementStatusUsers + 1 {
		recordStatus(t, appData, fmt.Sprintf("S-%d", i), start.Add(time.Duration(i)*time.Second),
			UserPlacementResult{Item: "a", Placement: "codex", Outcome: "installed"})
	}
	userPlacementStatusMu.Lock()
	status := loadUserPlacementStatus(appData)
	userPlacementStatusMu.Unlock()
	if len(status) != maxUserPlacementStatusUsers || status["S-0"] != nil || status[fmt.Sprintf("S-%d", maxUserPlacementStatusUsers)] == nil {
		t.Errorf("kept %d users, S-0 present %v; want %d without the oldest", len(status), status["S-0"] != nil, maxUserPlacementStatusUsers)
	}
}
