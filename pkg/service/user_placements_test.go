package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
)

// userPlanTable has one machine and one user placement on Windows; the user
// one is machine scope on macOS, so a darwin plan is empty.
var userPlanTable = placement.Table{
	Placements: map[string]placement.Spec{
		"claude": {Paths: map[string]string{placement.Windows: `C:\skills`}},
		"codex": {
			Paths: map[string]string{placement.Windows: `${USERPROFILE}\.agents\skills`, placement.Darwin: "/Library/codex"},
			Scope: map[string]string{placement.Windows: placement.ScopeUser},
		},
	},
	Order: []string{"claude", "codex"},
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func userPlanItem(name string, placements ...string) catalog.Item {
	variants := map[string]map[string]catalog.Variant{}
	for _, p := range placements {
		variants[p] = map[string]catalog.Variant{
			placement.Windows: {Location: "packages/" + name + "-" + p + ".zip", Hash: sha256Hex([]byte(name + p)), TreeHash: "sha256:" + name},
			placement.Darwin:  {Location: "packages/" + name + "-" + p + "-mac.zip", Hash: sha256Hex([]byte("mac")), TreeHash: "sha256:mac"},
		}
	}
	return catalog.Item{Name: name, Version: "1.0", Installer: catalog.InstallerItem{Type: "zip", Variants: variants}}
}

func userPlanCatalogs() map[int]map[string]catalog.Item {
	return map[int]map[string]catalog.Item{1: {
		"managed":      userPlanItem("managed", "claude", "codex"),
		"machine-only": userPlanItem("machine-only", "claude"),
		"dependency":   userPlanItem("dependency", "codex"),
		"held":         userPlanItem("held", "codex"),
		"held-managed": userPlanItem("held-managed", "codex"),
		"undesired":    userPlanItem("undesired", "codex"),
		"script":       {Name: "script", Installer: catalog.InstallerItem{Type: "ps1", Location: "x.ps1"}},
	}}
}

func set(names ...string) map[string]bool {
	s := map[string]bool{}
	for _, name := range names {
		s[name] = true
	}
	return s
}

// TestUserPlan: the plan is the run's desired set filtered to user-scope zip
// variants for the OS; a held name outside the desired set is listed held,
// and each archive is named after its hash, so a crafted location never
// shapes the staged path.
func TestUserPlan(t *testing.T) {
	desired := set("managed", "machine-only", "script", "held-managed", "dependency", "not-in-catalog")
	held := set("held", "held-managed")
	dir := filepath.Join("staged", "user-artifacts")

	managed := set("managed", "held-managed")
	plan := UserPlan(desired, held, managed, userPlanCatalogs(), userPlanTable, placement.Windows, dir)
	var got []string
	for _, e := range plan {
		got = append(got, e.Item+"/"+e.Placement)
		if e.Item == "held" != e.Held {
			t.Errorf("%s held = %v", e.Item, e.Held)
		}
		if managed[e.Item] != e.Managed {
			t.Errorf("%s managed = %v", e.Item, e.Managed)
		}
	}
	want := []string{"dependency/codex", "held/codex", "held-managed/codex", "managed/codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	m := plan[3]
	hash := sha256Hex([]byte("managedcodex"))
	if m.Version != "1.0" || m.Path != `${USERPROFILE}\.agents\skills` || m.Archive != filepath.Join(dir, hash+".zip") ||
		m.Hash != hash || m.TreeHash != "sha256:managed" || !m.Managed {
		t.Errorf("managed entry = %+v", m)
	}

	if again := UserPlan(desired, held, managed, userPlanCatalogs(), userPlanTable, placement.Windows, dir); !reflect.DeepEqual(again, plan) {
		t.Error("the same inputs gave a different plan")
	}
	if mac := UserPlan(desired, held, managed, userPlanCatalogs(), userPlanTable, placement.Darwin, dir); len(mac) != 0 {
		t.Errorf("darwin plan = %+v, want none (no user scope on macOS)", mac)
	}

	// A hash that is not a sha256 cannot name a staged file.
	bad := userPlanItem("bad", "codex")
	bad.Installer.Variants["codex"][placement.Windows] = catalog.Variant{Location: "packages/bad.zip", Hash: `..\..\evil`}
	if plan := UserPlan(set("bad"), nil, nil, map[int]map[string]catalog.Item{1: {"bad": bad}}, userPlanTable, placement.Windows, dir); len(plan) != 0 {
		t.Errorf("plan with a malformed hash = %+v, want none", plan)
	}
}

// TestUserPlanStore: the stored plan reads back as written, facts included,
// and a data directory without one is an empty plan.
func TestUserPlanStore(t *testing.T) {
	appData := filepath.Join(t.TempDir(), "data")
	if got, err := LoadUserPlan(appData); err != nil || got.Entries == nil || len(got.Entries) != 0 || got.Facts == nil {
		t.Fatalf("absent plan = %#v, %v; want empty", got, err)
	}
	entries := UserPlan(set("managed"), nil, set("managed"), userPlanCatalogs(), userPlanTable, placement.Windows, "dir")
	for i := range entries {
		entries[i].location = "" // not stored
	}
	plan := UserPlacements{Entries: entries, Facts: conditions.Facts{"os": "windows", "assigned_user": "jdoe"}}
	if err := SaveUserPlan(appData, plan); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUserPlan(appData); err != nil || !reflect.DeepEqual(got, plan) {
		t.Errorf("stored plan = %+v, %v; want %+v", got, err, plan)
	}
	if entries, _ := os.ReadDir(appData); len(entries) != 1 {
		t.Errorf("data directory holds %v, want only user-plan.json", entries)
	}
}

// TestStageUserArtifacts: the staging directory ends up holding exactly the
// non-held archives the plan references, each verified; a stale copy is
// replaced and anything unreferenced removed. With no plan and no directory,
// nothing is created.
func TestStageUserArtifacts(t *testing.T) {
	cache, dir := t.TempDir(), filepath.Join(t.TempDir(), "user-artifacts")
	z := &installer.ZipInstaller{CachePath: cache, URLPackages: "http://127.0.0.1:1/"}

	StageUserArtifacts(context.Background(), z, nil, dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("empty plan created %s: %v", dir, err)
	}

	// Verified copies already in the cache, so nothing is downloaded.
	archive := func(name, body string) UserPlacement {
		if err := os.MkdirAll(filepath.Join(cache, "packages"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, "packages", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		hash := sha256Hex([]byte(body))
		return UserPlacement{Item: name, Placement: "codex", Archive: filepath.Join(dir, hash+".zip"), Hash: hash, location: "packages/" + name}
	}
	fresh, stale, held := archive("fresh.zip", "fresh"), archive("stale.zip", "new"), archive("held.zip", "held")
	held.Held = true
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{stale.Archive: "old", filepath.Join(dir, "unreferenced.zip"): "x"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	StageUserArtifacts(context.Background(), z, []UserPlacement{fresh, stale, held}, dir)
	for path, want := range map[string]string{fresh.Archive: "fresh", stale.Archive: "new"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", path, data, err, want)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("user-artifacts holds %v, want only the fresh and stale archives", entries)
	}
}

// TestUserPlacementMethods: listUserPlacements serves the plan the last run
// stored, without reading manifests, catalogs or the placement table (the
// repository may be unreachable at logon); with none stored it answers an
// empty plan. reportUserPlacements takes the helper's results and answers {}.
func TestUserPlacementMethods(t *testing.T) {
	origManifest, origCatalog, origPlacements := manifestGet, catalogGet, placementsGet
	t.Cleanup(func() { manifestGet, catalogGet, placementsGet = origManifest, origCatalog, origPlacements })
	errRepository := errors.New("repository read")
	manifestGet = func(config.Configuration) ([]manifest.Item, []string, error) {
		t.Error("listUserPlacements read the manifests")
		return nil, nil, errRepository
	}
	catalogGet = func(config.Configuration) (map[int]map[string]catalog.Item, error) {
		t.Error("listUserPlacements read the catalogs")
		return nil, errRepository
	}
	placementsGet = func(config.Configuration) (placement.Table, error) {
		t.Error("listUserPlacements read the placement table")
		return placement.Table{}, errRepository
	}

	cfg := testServiceConfig(t)
	client := NewClient(cfg.ServicePipeName)
	client.ConnectTimeout = 100 * time.Millisecond
	ctx := context.Background()
	// A service not yet listening is a connection failure, which the helper
	// retries at logon.
	if _, err := client.ListUserPlacements(ctx); !errors.Is(err, ErrConnect) {
		t.Errorf("ListUserPlacements before the service listens: %v, want ErrConnect", err)
	}
	startTestRunner(t, cfg, noopRun)

	info, infoErr := client.GetServiceInfo(ctx)
	if infoErr != nil {
		t.Fatal(infoErr)
	}
	for _, method := range []string{methodListUserPlacements, methodReportUserPlacements} {
		if !slices.Contains(info.Capabilities, method) {
			t.Errorf("capabilities %v lack %s", info.Capabilities, method)
		}
	}

	// Before the first run: an empty plan, not an error.
	if got, err := client.ListUserPlacements(ctx); err != nil || len(got.Entries) != 0 {
		t.Fatalf("ListUserPlacements before a run = %+v, %v; want empty", got, err)
	}

	entries := UserPlan(set("managed", "dependency"), nil, set("managed"), userPlanCatalogs(), userPlanTable, placement.Windows, "dir")
	for i := range entries {
		entries[i].location = ""
	}
	stored := UserPlacements{Entries: entries, Facts: conditions.Facts{"os": "windows"}}
	if err := SaveUserPlan(cfg.AppDataPath, stored); err != nil {
		t.Fatal(err)
	}
	// Two calls: the same plan, facts included, whoever asks.
	for range 2 {
		got, err := client.ListUserPlacements(ctx)
		if err != nil {
			t.Fatalf("ListUserPlacements: %v", err)
		}
		if !reflect.DeepEqual(got, stored) {
			t.Fatalf("plan = %+v, want the stored %+v", got, stored)
		}
	}

	if err := client.ReportUserPlacements(ctx, []UserPlacementResult{{Item: "managed", Placement: "codex", Outcome: "installed"}}); err != nil {
		t.Errorf("ReportUserPlacements: %v", err)
	}
	resp := readResponse(t, rawExchange(t, cfg, `{"jsonrpc":"2.0","id":"1","method":"reportUserPlacements","params":{}}`))
	if resp.Error == nil || resp.Error.Data.Code != "invalid_params" {
		t.Errorf("report without results = %+v, want invalid_params", resp)
	}

	// The -S commands that mirror the two methods.
	listed, err := SendCommand(cfg, "listuserplacements")
	if err != nil || len(listed.Items) != 2 || !strings.Contains(listed.Items[0], `"item":"dependency"`) || !strings.Contains(listed.Items[0], `"held":false`) ||
		!strings.Contains(listed.Items[0], `"managed":false`) || !strings.Contains(listed.Items[1], `"managed":true`) {
		t.Errorf("-S ListUserPlacements = %+v, %v", listed.Items, err)
	}
	if _, err := SendCommand(cfg, "ReportUserPlacements:managed,codex,failed,boom"); err != nil {
		t.Errorf("-S ReportUserPlacements: %v", err)
	}
	for _, spec := range []string{"ReportUserPlacements:managed,codex", "ListUserPlacements:extra"} {
		if _, err := parseCommandSpec(spec); err == nil {
			t.Errorf("%s accepted", spec)
		}
	}
}

// TestUserArtifactsDir: the staging directory sits inside the data directory
// the config names, never beside the executable, so it moves with
// app_data_path and the protection of the data directory covers it.
func TestUserArtifactsDir(t *testing.T) {
	data := filepath.Join("pd", "Acme", "Data")
	if got, want := UserArtifactsDir(data), filepath.Join(data, "user-artifacts"); got != want {
		t.Errorf("UserArtifactsDir = %s, want %s", got, want)
	}
}
