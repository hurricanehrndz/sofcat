package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
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
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
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

// TestUserPlanStore: the stored plan reads back as written, facts and the
// user-scope specs included, and a data directory without one is an empty
// plan.
func TestUserPlanStore(t *testing.T) {
	appData := filepath.Join(t.TempDir(), "data")
	if got, err := LoadUserPlan(appData); err != nil || got.Entries == nil || len(got.Entries) != 0 || got.Facts == nil || got.Placements == nil {
		t.Fatalf("absent plan = %#v, %v; want empty", got, err)
	}
	table := placement.Table{Placements: maps.Clone(userPlanTable.Placements), Order: slices.Clone(userPlanTable.Order)}
	codex := table.Placements["codex"]
	codex.Exclusive, codex.ItemsDir = true, "plugins"
	codex.Vars = map[string]any{"name": "acme"}
	codex.Files = []placement.File{{Path: "index.json", Template: "x"}}
	table.Placements["codex"] = codex
	entries := UserPlan(set("managed"), nil, set("managed"), userPlanCatalogs(), table, placement.Windows, "dir")
	for i := range entries {
		entries[i].location = "" // not stored
	}
	specs := UserSpecs(table, placement.Windows)
	if !reflect.DeepEqual(specs, map[string]placement.Spec{"codex": codex}) {
		t.Fatalf("user specs = %+v, want codex only", specs)
	}
	plan := UserPlacements{
		Entries: entries, Facts: conditions.Facts{"os": "windows", "assigned_user": "jdoe"}, Placements: specs,
		UserFiles: map[string]UserFileSet{"market": {Spec: placement.Spec{Files: []placement.File{{Paths: map[string]string{placement.Windows: "x"}, Scope: placement.ScopeUser, Merge: placement.MergeAddIfAbsent, Template: "{}"}}}, Root: `C:\market`, Items: []render.Item{{Name: "a"}}}},
	}
	if err := SaveUserPlan(appData, plan); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUserPlan(appData); err != nil || !reflect.DeepEqual(got, plan) {
		t.Errorf("stored plan = %+v, %v; want %+v", got, err, plan)
	}
	if entries, _ := os.ReadDir(appData); len(entries) != 1 {
		t.Errorf("data directory holds %v, want only user-plan.json", entries)
	}

	// No placements are stored as {}, not null, so only an older service's
	// plan lacks them.
	if err := SaveUserPlan(appData, UserPlacements{}); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUserPlan(appData); err != nil || got.Placements == nil || len(got.Placements) != 0 || got.UserFiles == nil || len(got.UserFiles) != 0 {
		t.Errorf("plan without user placements = %#v, %v; want empty placements and user files", got, err)
	}
}

// TestUserSyncDue: every run asks whether to start the per-user helpers.
// They start once per plan, not once per scheduled run, and again for a
// plan they have not been started for, including after a run that could not
// mark it; before any plan is stored there is nothing to start them for.
func TestUserSyncDue(t *testing.T) {
	appData := t.TempDir()
	if _, due := userSyncDue(appData); due {
		t.Fatal("due with no stored plan")
	}
	plan := UserPlacements{Facts: conditions.Facts{"os": "windows"}}
	if err := SaveUserPlan(appData, plan); err != nil {
		t.Fatal(err)
	}
	hash, due := userSyncDue(appData)
	if !due {
		t.Fatal("a new plan is not due")
	}
	if _, due := userSyncDue(appData); !due {
		t.Fatal("an unmarked plan stopped being due")
	}
	if err := markUserSyncStarted(appData, hash); err != nil {
		t.Fatal(err)
	}
	if _, due := userSyncDue(appData); due {
		t.Fatal("due after marking it started")
	}
	if err := SaveUserPlan(appData, plan); err != nil {
		t.Fatal(err)
	}
	if _, due := userSyncDue(appData); due {
		t.Fatal("the same plan saved again is due")
	}
	plan.Facts["os"] = "macos"
	if err := SaveUserPlan(appData, plan); err != nil {
		t.Fatal(err)
	}
	if _, due := userSyncDue(appData); !due {
		t.Fatal("a changed plan is not due")
	}
}

// TestUserPlanStoreFromOlderService: a plan stored before the plan carried
// specs still loads, with Placements nil, which tells the helper to
// synthesize them.
func TestUserPlanStoreFromOlderService(t *testing.T) {
	appData := t.TempDir()
	old := `{"entries":[{"item":"skill","version":"1.0","placement":"codex","path":"${USERPROFILE}\\.agents\\skills","archive":"a.zip","hash":"h","treeHash":"t","held":false,"managed":true}],"facts":{"os":"windows"}}`
	if err := os.WriteFile(UserPlanPath(appData), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadUserPlan(appData)
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Item != "skill" || got.Facts["os"] != "windows" || got.Placements != nil {
		t.Errorf("older plan = %#v, %v; want its entry, facts and nil placements", got, err)
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

	// Neither cached nor reachable: its fetch fails.
	missingHash := sha256Hex([]byte("missing"))
	missing := UserPlacement{Item: "missing.zip", Placement: "codex", Archive: filepath.Join(dir, missingHash+".zip"), Hash: missingHash, location: "packages/missing.zip"}

	plan := []UserPlacement{fresh, stale, held, missing}
	StageUserArtifacts(context.Background(), z, plan, dir)
	for path, want := range map[string]string{fresh.Archive: "fresh", stale.Archive: "new"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", path, data, err, want)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("user-artifacts holds %v, want only the fresh and stale archives", entries)
	}
	// An entry whose archive did not stage is saved held, so the helper
	// keeps the user's current tree instead of removing it over a download
	// that may work next run; the staged ones stay as planned.
	for i, want := range []bool{false, false, true, true} {
		if plan[i].Held != want {
			t.Errorf("%s held = %v, want %v", plan[i].Item, plan[i].Held, want)
		}
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
	stored := UserPlacements{Entries: entries, Facts: conditions.Facts{"os": "windows"}, Placements: UserSpecs(userPlanTable, placement.Windows), UserFiles: map[string]UserFileSet{}}
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
	// A plan an older service stored reaches the helper with nil placements,
	// not empty ones.
	if err := os.WriteFile(UserPlanPath(cfg.AppDataPath), []byte(`{"entries":[],"facts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := client.ListUserPlacements(ctx); err != nil || got.Placements != nil {
		t.Errorf("older plan over the pipe = %#v, %v; want nil placements", got, err)
	}
	if err := SaveUserPlan(cfg.AppDataPath, stored); err != nil {
		t.Fatal(err)
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
	if err != nil || len(listed.Items) != 3 || !strings.Contains(listed.Items[0], `"item":"dependency"`) || !strings.Contains(listed.Items[0], `"held":false`) ||
		!strings.Contains(listed.Items[0], `"managed":false`) || !strings.Contains(listed.Items[1], `"managed":true`) ||
		!strings.HasPrefix(listed.Items[2], `{"placements":{"codex":`) {
		t.Errorf("-S ListUserPlacements = %+v, %v", listed.Items, err)
	}
	// The report is stored under the caller the connection resolved, for
	// the storefront's status of user-scope items: the test's own user,
	// whatever form its ID takes on this OS.
	userPlacementStatusMu.Lock()
	statuses := loadUserPlacementStatus(cfg.AppDataPath)
	userPlacementStatusMu.Unlock()
	var caller string
	for id := range statuses {
		caller = id
	}
	if len(statuses) != 1 || caller == "" || statuses[caller]["managed"]["codex"].Outcome != "installed" {
		t.Errorf("stored status after the report = %+v, want installed for the caller alone", statuses)
	}
	if _, err := SendCommand(cfg, "ReportUserPlacements:managed,codex,failed,boom"); err != nil {
		t.Errorf("-S ReportUserPlacements: %v", err)
	}
	if got := userPlacementStatusFor(cfg.AppDataPath, caller)["managed"]["codex"].Outcome; got != "failed" {
		t.Errorf("stored outcome after the -S report = %q, want failed", got)
	}
	for _, spec := range []string{"ReportUserPlacements:managed,codex", "ListUserPlacements:extra"} {
		if _, err := parseCommandSpec(spec); err == nil {
			t.Errorf("%s accepted", spec)
		}
	}
}

// TestUserPlanCarriesTemplateFileText: the helper never fetches, so the
// stored plan holds a user-scope template_file entry as its resolved text.
func TestUserPlanCarriesTemplateFileText(t *testing.T) {
	const text = `{"n": {{ len .Items }}}`
	table, err := placement.Parse([]byte(`placements:
  codex:
    paths: {windows: '${USERPROFILE}\.agents\skills'}
    scope: {windows: user}
    files:
      - {path: .index.json, template_file: templates/index.json.tmpl}
`), func(string) ([]byte, error) { return []byte(text), nil })
	if err != nil {
		t.Fatal(err)
	}
	appData := t.TempDir()
	if err = SaveUserPlan(appData, UserPlacements{Placements: UserSpecs(table, placement.Windows)}); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(UserPlanPath(appData))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "template_file") || strings.Contains(string(stored), "index.json.tmpl") {
		t.Errorf("stored plan names the template file:\n%s", stored)
	}
	plan, err := LoadUserPlan(appData)
	if err != nil {
		t.Fatal(err)
	}
	want := []placement.File{{Path: ".index.json", Template: text}}
	if got := plan.Placements["codex"].Files; !reflect.DeepEqual(got, want) {
		t.Errorf("stored files = %+v, want %+v", got, want)
	}
}

// TestUserFileSets: a machine placement with a scope: user files entry is
// handed to the helper with its root and the items its receipts describe,
// sorted by name; with nothing placed the set is still there, with empty
// items, so the helper removes the keys. Placements without such an entry,
// and user-scope ones, are left out, and a root the guard refuses is held.
func TestUserFileSets(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "roots", "market")
	userFile := placement.File{
		Paths: map[string]string{placement.Windows: `${USERPROFILE}\.claude\settings.json`},
		Scope: placement.ScopeUser, Merge: placement.MergeAddIfAbsent, Template: "{}",
	}
	machineFile := placement.File{Paths: map[string]string{placement.Windows: filepath.Join(base, "roots", "m.json")}, Template: "{}"}
	paths := func(dir string) map[string]string { return map[string]string{placement.Windows: dir} }
	table := placement.Table{
		Placements: map[string]placement.Spec{
			"market":  {Paths: paths(root), Files: []placement.File{machineFile, userFile}},
			"empty":   {Paths: paths(filepath.Join(base, "roots", "empty")), Files: []placement.File{userFile}},
			"outside": {Paths: paths(filepath.Join(base, "elsewhere")), Files: []placement.File{userFile}},
			"plain":   {Paths: paths(filepath.Join(base, "roots", "plain")), Files: []placement.File{machineFile}},
			"user":    {Paths: paths(`${USERPROFILE}\skills`), Scope: map[string]string{placement.Windows: placement.ScopeUser}, Files: []placement.File{userFile}},
		},
		Order: []string{"market", "empty", "outside", "plain", "user"},
	}
	receipts := filepath.Join(base, "receipts")
	for _, item := range []string{"b", "a"} {
		if err := receipt.Write(receipts, receipt.Receipt{Item: item, Version: "1.0", Placement: "market", Path: filepath.Join(root, item), TreeHash: "t", PlacedHash: "p", Managed: item == "a"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := receipt.Write(receipts, receipt.Receipt{Item: "z", Placement: "plain", Path: "p", TreeHash: "t", PlacedHash: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	z := &installer.ZipInstaller{
		Guard:      placement.Guard{GOOS: placement.Windows, Roots: placement.Roots{Windows: []string{filepath.Join(base, "roots")}}},
		Table:      table,
		ReceiptDir: receipts,
	}

	sets := UserFileSets(z, table, placement.Windows)
	if got := slices.Sorted(maps.Keys(sets)); !slices.Equal(got, []string{"empty", "market", "outside"}) {
		t.Fatalf("sets for %v, want empty, market, outside", got)
	}
	market := sets["market"]
	wantItems := []render.Item{
		{Name: "a", Version: "1.0", Managed: true, Path: filepath.Join(root, "a"), TreeHash: "t", PlacedHash: "p"},
		{Name: "b", Version: "1.0", Path: filepath.Join(root, "b"), TreeHash: "t", PlacedHash: "p"},
	}
	if market.Held || market.Root != root || !reflect.DeepEqual(market.Items, wantItems) || !reflect.DeepEqual(market.Spec, table.Placements["market"]) {
		t.Errorf("market = %+v, want root %s, items %+v and its whole spec", market, root, wantItems)
	}
	if empty := sets["empty"]; empty.Held || empty.Items == nil || len(empty.Items) != 0 {
		t.Errorf("empty = %+v, want present with empty, non-nil items", empty)
	}
	if outside := sets["outside"]; !outside.Held || outside.Items == nil {
		t.Errorf("outside = %+v, want held", outside)
	}

	// Through the plan file, as the helper reads it.
	appData := filepath.Join(base, "data")
	if err := SaveUserPlan(appData, UserPlacements{UserFiles: sets}); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUserPlan(appData); err != nil || !reflect.DeepEqual(got.UserFiles, sets) {
		t.Errorf("stored user files = %+v, %v; want %+v", got.UserFiles, err, sets)
	}
}
