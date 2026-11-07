package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/service"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
	"github.com/hurricanehrndz/sofcat/pkg/software"
	"github.com/hurricanehrndz/sofcat/pkg/usersync"
	"go.yaml.in/yaml/v4"
)

// tapasOS is the placement OS the end-to-end tests place for. Windows keys work
// on every host (paths are absolute either way, every file hashes as 644,
// comparisons fold case) and, unlike darwin, allow user-scope placements.
const tapasOS = placement.Windows

// tapasRepo is a sofcat repository served over HTTP: placements.yaml,
// catalogs, manifests and zip archives built from in-test trees. Files can be
// replaced or withdrawn between runs.
type tapasRepo struct {
	t     *testing.T
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string][]byte // URL path without the leading slash
	items map[string]catalog.Item
	// paths holds each placement's path from the last servePlacements call.
	paths map[string]string
}

func newTapasRepo(t *testing.T) *tapasRepo {
	t.Helper()
	r := &tapasRepo{t: t, files: map[string][]byte{}, items: map[string]catalog.Item{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		body, ok := r.files[strings.TrimPrefix(req.URL.Path, "/")]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// URL is the repository root, with the trailing slash sofcat expects.
func (r *tapasRepo) URL() string { return r.srv.URL + "/" }

// put serves body at path; a nil body withdraws it (404).
func (r *tapasRepo) put(path string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if body == nil {
		delete(r.files, path)
		return
	}
	r.files[path] = body
}

// zipItem packs one tree per placement for tapasOS, serves the archives, and
// adds the item to the catalog published by publishCatalog.
func (r *tapasRepo) zipItem(name, version string, trees map[string]map[string]string) {
	r.t.Helper()
	variants := map[string]map[string]catalog.Variant{}
	for _, placementName := range slices.Sorted(maps.Keys(trees)) {
		zipPath := filepath.Join(tempDir(r.t), "a.zip")
		packed, err := artifact.PackFile(writeFiles(r.t, tempDir(r.t), trees[placementName]), tapasOS, zipPath)
		if err != nil {
			r.t.Fatal(err)
		}
		data, err := os.ReadFile(zipPath)
		if err != nil {
			r.t.Fatal(err)
		}
		location := fmt.Sprintf("packages/%s-%s-%s.zip", name, version, packed.TreeHash[7:19])
		r.put(location, data)
		variants[placementName] = map[string]catalog.Variant{tapasOS: {Location: location, Hash: packed.Hash, TreeHash: packed.TreeHash}}
	}
	r.items[name] = catalog.Item{Name: name, DisplayName: name, Version: version, Installer: catalog.InstallerItem{Type: "zip", Variants: variants}}
}

// publishCatalog serves every item added so far as catalogs/tapas.yaml, the
// one catalog tapasEnv configures.
func (r *tapasRepo) publishCatalog() {
	r.t.Helper()
	data, err := yaml.Marshal(r.items)
	if err != nil {
		r.t.Fatal(err)
	}
	r.put("catalogs/tapas.yaml", data)
}

// servePlacements serves placements.yaml with one directory placement per entry of
// paths; exclusive and user name placements that get those settings.
func (r *tapasRepo) servePlacements(paths map[string]string, exclusive, user []string) {
	var b strings.Builder
	b.WriteString("placements:\n")
	for _, name := range slices.Sorted(maps.Keys(paths)) {
		fmt.Fprintf(&b, "  %s:\n    kind: directory\n    paths: {%s: '%s'}\n", name, tapasOS, paths[name])
		if slices.Contains(exclusive, name) {
			b.WriteString("    exclusive: true\n")
		}
		if slices.Contains(user, name) {
			fmt.Fprintf(&b, "    scope: {%s: user}\n", tapasOS)
		}
	}
	if _, err := placement.Parse([]byte(b.String())); err != nil {
		r.t.Fatalf("fixture placements.yaml invalid: %v\n%s", err, b.String())
	}
	r.put("placements.yaml", []byte(b.String()))
	r.paths = paths
}

func writeFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// tapasEnv is one machine: write roots, a data directory and a binary
// directory under a temp dir, and managedRun pointed at a tapasRepo.
type tapasEnv struct {
	*tapasRepo
	t     *testing.T
	cfg   config.Configuration
	roots string // the single write root
	bin   string // stands in for the directory holding sofcat's binaries
	// userArtifacts is the service's user-artifacts staging directory.
	userArtifacts string
}

func newTapasEnv(t *testing.T) *tapasEnv {
	t.Helper()
	resetMainHooks()
	t.Cleanup(resetMainHooks)
	base := tempDir(t)
	e := &tapasEnv{
		tapasRepo:     newTapasRepo(t),
		t:             t,
		roots:         filepath.Join(base, "roots"),
		bin:           filepath.Join(base, "roots", "apps", "bin"),
		userArtifacts: filepath.Join(base, "data", "user-artifacts"),
	}
	for _, dir := range []string{e.roots, e.bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.cfg = config.Configuration{
		AppDataPath: filepath.Join(base, "data"),
		CachePath:   filepath.Join(base, "cache"),
		URL:         e.URL(),
		URLPackages: e.URL(),
		Manifest:    "tapas",
		Catalogs:    []string{"tapas"},
	}
	adminCheckFunc = func() (bool, error) { return true, nil }
	origUserArtifacts := service.UserArtifactsDir
	service.UserArtifactsDir = func(string) string { return e.userArtifacts }
	t.Cleanup(func() { service.UserArtifactsDir = origUserArtifacts })
	newZipInstaller = func(cfg config.Configuration, table placement.Table) *installer.ZipInstaller {
		z := installer.NewMachineZip(cfg, table)
		z.Guard = placement.Guard{
			GOOS:       tapasOS,
			Roots:      placement.Roots{Windows: []string{e.roots}},
			DataDir:    cfg.AppDataPath,
			BinaryDirs: []string{e.bin},
		}
		z.Slug = "tapas-test"
		return z
	}
	return e
}

func (e *tapasEnv) receiptDir() string { return receipt.MachineDir(e.cfg.AppDataPath) }

// run does one managed run and returns its report.
func (e *tapasEnv) run() *report.Report {
	e.t.Helper()
	// Close the log before t.TempDir cleanup (see managed_run_test.go).
	defer sofcatlog.Close()
	rep, err := managedRun(e.cfg, nil, nil)
	if err != nil {
		e.t.Fatalf("managedRun: %v", err)
	}
	return rep
}

// seed places files as item's tree in placement (as servePlacements set it), as an earlier run or
// a user would have, and returns its tree hash. With a version it also writes
// the receipt sofcat would have written.
func (e *tapasEnv) seed(placementName, item, version string, files map[string]string) string {
	e.t.Helper()
	tree := writeFiles(e.t, filepath.Join(e.paths[placementName], item), files)
	hash, err := artifact.TreeHash(tree, tapasOS)
	if err != nil {
		e.t.Fatal(err)
	}
	if version != "" {
		err := receipt.Write(e.receiptDir(), receipt.Receipt{
			Item: item, Version: version, Placement: placementName, Path: tree,
			TreeHash: hash, InstalledAtUTC: time.Now().UTC(),
		})
		if err != nil {
			e.t.Fatal(err)
		}
	}
	return hash
}

func failedCodes(rep *report.Report, item string) []string {
	var codes []string
	for _, f := range rep.FailedItems {
		if f.Name == item {
			codes = append(codes, f.Code)
		}
	}
	return codes
}

func listed(items []catalog.Item, name string) bool {
	return slices.ContainsFunc(items, func(i catalog.Item) bool { return i.Name == name })
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// TestTapasZipPlacements runs managed runs against a served repository of zip
// items and checks what lands on disk, the receipts, and the report codes the
// UI shows: installs, adoption, user edits left alone, scope exit, held items,
// refusals, and user-scope placements skipped by the machine run.
func TestTapasZipPlacements(t *testing.T) {
	e := newTapasEnv(t)
	skills := filepath.Join(e.roots, "skills")
	market := filepath.Join(e.roots, "market")
	outside := tempDir(t)
	escape := filepath.Join(e.roots, "link")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	e.servePlacements(map[string]string{
		"skills":  skills,
		"market":  market,
		"escape":  filepath.Join(escape, "skills"),
		"binary":  filepath.Join(e.roots, "apps"),
		"profile": `${USERPROFILE}\.agents\skills`,
	}, []string{"market"}, []string{"profile"})

	skill := func(body string) map[string]string {
		return map[string]string{"SKILL.md": body, "ref/notes.md": "notes"}
	}
	e.zipItem("hello", "1.0", map[string]map[string]string{"skills": skill("hello")})
	e.zipItem("adopt", "1.0", map[string]map[string]string{"skills": skill("adopt")})
	e.zipItem("edit", "1.0", map[string]map[string]string{"skills": skill("edit v1")})
	e.zipItem("both", "1.0", map[string]map[string]string{"skills": skill("both"), "profile": skill("both")})
	e.zipItem("exclusive", "1.0", map[string]map[string]string{"market": skill("x")})
	e.zipItem("escape", "1.0", map[string]map[string]string{"escape": skill("x")})
	e.zipItem("binary", "1.0", map[string]map[string]string{"binary": skill("x")})
	e.zipItem("held", "1.0", map[string]map[string]string{"skills": skill("held")})
	e.zipItem("dropme", "1.0", map[string]map[string]string{"skills": skill("dropme")})
	e.publishCatalog()
	e.put("manifests/tapas.yaml", []byte(`name: tapas
catalogs: [tapas]
managed_installs: [hello, adopt, edit, both, exclusive, escape, binary]
managed_uninstalls: [dropme, orphan]
conditional_items:
  - condition: '"lab" in machine_groups'
    managed_installs: [held]
`))

	// Before the run: a catalog-equal tree sofcat never recorded, trees an
	// earlier run placed for a held item, a removed item, items to remove
	// that no catalog lists any more, items that left the manifest (one since
	// edited), and someone else's files in the exclusive root.
	e.seed("skills", "adopt", "", skill("adopt"))
	e.seed("skills", "held", "1.0", skill("held"))
	e.seed("skills", "dropme", "1.0", skill("dropme"))
	e.seed("skills", "orphan", "1.0", skill("orphan"))
	e.seed("skills", "ss-orphan", "1.0", skill("ss-orphan"))
	if err := manifest.SaveSelfServe(manifest.SelfServePath(e.cfg.AppDataPath), manifest.Item{Uninstalls: []string{"ss-orphan"}}); err != nil {
		t.Fatal(err)
	}
	e.seed("skills", "gone", "0.9", skill("gone"))
	e.seed("skills", "gone-edited", "0.9", skill("gone-edited"))
	if err := os.WriteFile(filepath.Join(skills, "gone-edited", "SKILL.md"), []byte("my notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.seed("market", "theirs", "", map[string]string{"plugin.json": "{}"})

	rep := e.run()

	// Installed into an empty root, with a receipt flagged managed.
	if readFile(t, filepath.Join(skills, "hello", "SKILL.md")) != "hello" || !listed(rep.InstalledItems, "hello") {
		t.Errorf("hello not installed: %+v", rep)
	}
	rc, ok, err := receipt.Read(e.receiptDir(), "skills", "hello")
	if err != nil || !ok || !rc.Managed || rc.Version != "1.0" || rc.Path != filepath.Join(skills, "hello") {
		t.Errorf("hello receipt = %+v, %v, %v", rc, ok, err)
	}

	// Adopted: a receipt, and no failure.
	if _, ok, _ := receipt.Read(e.receiptDir(), "skills", "adopt"); !ok || failedCodes(rep, "adopt") != nil {
		t.Errorf("adopt not adopted: receipt %v, failures %v", ok, failedCodes(rep, "adopt"))
	}

	// A user-scope placement is skipped: the item succeeds on its machine one.
	if !listed(rep.InstalledItems, "both") || failedCodes(rep, "both") != nil {
		t.Errorf("both: installed %v, failures %v", listed(rep.InstalledItems, "both"), failedCodes(rep, "both"))
	}
	if _, ok, _ := receipt.Read(e.receiptDir(), "profile", "both"); ok {
		t.Error("machine run wrote a user-scope receipt")
	}

	// Refusals: someone else's exclusive root, a symlink out of the roots,
	// a placement around the binary directory.
	for _, name := range []string{"exclusive", "escape", "binary"} {
		if codes := failedCodes(rep, name); !slices.Equal(codes, []string{report.CodePlacementRefused}) {
			t.Errorf("%s codes = %v, want placement_refused", name, codes)
		}
	}
	if exists(filepath.Join(outside, "skills")) || exists(filepath.Join(market, "exclusive")) || exists(filepath.Join(e.roots, "apps", "binary")) {
		t.Error("a refused placement wrote files")
	}

	// managed_uninstalls removes a zip item through its receipt.
	if exists(filepath.Join(skills, "dropme")) || !listed(rep.UninstalledItems, "dropme") {
		t.Error("dropme not removed")
	}
	// An item to remove (managed_uninstalls or a pending self-service
	// removal) that no catalog lists any more: the uninstall pass cannot
	// resolve it, so scope exit removes it by its receipt.
	for _, name := range []string{"orphan", "ss-orphan"} {
		if exists(filepath.Join(skills, name)) || !listed(rep.UninstalledItems, name) {
			t.Errorf("%s not removed by scope exit", name)
		}
		if _, ok, _ := receipt.Read(e.receiptDir(), "skills", name); ok {
			t.Errorf("%s's receipt kept", name)
		}
	}

	// Scope exit: an item that left the desired set is removed; an edited one
	// stays with a conflict; a held item stays untouched.
	if exists(filepath.Join(skills, "gone")) || !listed(rep.UninstalledItems, "gone") {
		t.Error("gone not removed by scope exit")
	}
	if _, ok, _ := receipt.Read(e.receiptDir(), "skills", "gone"); ok {
		t.Error("gone's receipt kept")
	}
	if readFile(t, filepath.Join(skills, "gone-edited", "SKILL.md")) != "my notes" ||
		!slices.Equal(failedCodes(rep, "gone-edited"), []string{report.CodePlacementConflict}) {
		t.Errorf("gone-edited: codes %v", failedCodes(rep, "gone-edited"))
	}
	if !exists(filepath.Join(skills, "held", "SKILL.md")) || listed(rep.UninstalledItems, "held") {
		t.Error("held item removed")
	}
	if len(rep.HeldConditions) != 1 {
		t.Errorf("held conditions = %+v", rep.HeldConditions)
	}

	// The next release of edit meets a user's edit: conflict, tree untouched.
	editFile := filepath.Join(skills, "edit", "SKILL.md")
	if readFile(t, editFile) != "edit v1" {
		t.Fatal("edit v1 not installed")
	}
	if err := os.WriteFile(editFile, []byte("edited by me"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.zipItem("edit", "2.0", map[string]map[string]string{"skills": skill("edit v2")})
	e.zipItem("hello", "2.0", map[string]map[string]string{"skills": skill("hello v2")})
	e.publishCatalog()

	rep = e.run()
	if !slices.Equal(failedCodes(rep, "edit"), []string{report.CodePlacementConflict}) || readFile(t, editFile) != "edited by me" {
		t.Errorf("edit before upgrade: codes %v, content %q", failedCodes(rep, "edit"), readFile(t, editFile))
	}
	// An untouched tree is upgraded in place.
	if readFile(t, filepath.Join(skills, "hello", "SKILL.md")) != "hello v2" {
		t.Error("hello not upgraded")
	}
	if rc, _, _ := receipt.Read(e.receiptDir(), "skills", "hello"); rc.Version != "2.0" {
		t.Errorf("hello receipt version = %q", rc.Version)
	}
}

// TestTapasMissingPlacements: a repository without placements.yaml still runs.
// Items that are not zip items are unaffected, and every zip item fails with
// placement_refused because no placement is defined.
func TestTapasMissingPlacements(t *testing.T) {
	e := newTapasEnv(t)
	e.zipItem("hello", "1.0", map[string]map[string]string{"skills": {"SKILL.md": "x"}})
	present := filepath.Join(tempDir(t), "present.txt")
	writeFiles(t, filepath.Dir(present), map[string]string{"present.txt": "x"})
	e.items["Tool"] = catalog.Item{
		Name: "Tool", DisplayName: "Tool",
		Check:     catalog.InstallCheck{File: []catalog.FileCheck{{Path: present}}},
		Installer: catalog.InstallerItem{Type: "ps1", Location: "packages/tool.ps1", Hash: "deadbeef"},
	}
	e.publishCatalog()
	e.put("manifests/tapas.yaml", []byte("name: tapas\ncatalogs: [tapas]\nmanaged_installs: [hello, Tool]\n"))

	rep := e.run()
	if codes := failedCodes(rep, "hello"); !slices.Equal(codes, []string{report.CodePlacementRefused}) {
		t.Errorf("hello codes = %v, want placement_refused", codes)
	}
	if codes := failedCodes(rep, "Tool"); codes != nil {
		t.Errorf("Tool failed without placements.yaml: %v", rep.FailedItems)
	}
}

// TestTapasBadArchive: an archive that fails verification is never placed.
// Served bytes that do not match the variant hash, or a good archive whose
// contents do not match the catalog tree_hash, fail the item and leave no
// tree, no receipt and no staging directory behind.
func TestTapasBadArchive(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(e *tapasEnv, v catalog.Variant)
	}{
		{"served bytes do not match hash", func(e *tapasEnv, v catalog.Variant) {
			e.put(v.Location, []byte("not the archive the catalog names"))
		}},
		{"tree_hash does not match the archive", func(e *tapasEnv, v catalog.Variant) {
			other, err := artifact.TreeHash(writeFiles(t, tempDir(t), map[string]string{"SKILL.md": "other"}), tapasOS)
			if err != nil {
				t.Fatal(err)
			}
			item := e.items["hello"]
			v.TreeHash = other
			item.Installer.Variants["skills"][tapasOS] = v
			e.items["hello"] = item
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTapasEnv(t)
			skills := filepath.Join(e.roots, "skills")
			e.servePlacements(map[string]string{"skills": skills}, nil, nil)
			e.zipItem("hello", "1.0", map[string]map[string]string{"skills": {"SKILL.md": "hello"}})
			tc.corrupt(e, e.items["hello"].Installer.Variants["skills"][tapasOS])
			e.publishCatalog()
			e.put("manifests/tapas.yaml", []byte("name: tapas\ncatalogs: [tapas]\nmanaged_installs: [hello]\n"))

			rep := e.run()
			if codes := failedCodes(rep, "hello"); len(codes) != 1 || codes[0] != "" || listed(rep.InstalledItems, "hello") {
				t.Errorf("hello: codes %q, installed %v; want one generic item failure", codes, listed(rep.InstalledItems, "hello"))
			}
			entries, err := os.ReadDir(skills)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("placement root holds %v after a failed verification", entries)
			}
			if _, ok, _ := receipt.Read(e.receiptDir(), "skills", "hello"); ok {
				t.Error("receipt written for an unverified archive")
			}
		})
	}
}

// tapasUserClient stands in for the service client of the per-user helper:
// its plan is the one the last run stored, which listUserPlacements serves,
// and it keeps what the helper reports.
type tapasUserClient struct {
	cfg      config.Configuration
	reported []service.UserPlacementResult
}

func (c *tapasUserClient) ListUserPlacements(context.Context) (service.UserPlacements, error) {
	return service.LoadUserPlan(c.cfg.AppDataPath)
}

func (c *tapasUserClient) ReportUserPlacements(_ context.Context, results []service.UserPlacementResult) error {
	c.reported = append(c.reported, results...)
	return nil
}

func outcomeOf(results []service.UserPlacementResult, item, placementName string) string {
	for _, r := range results {
		if r.Item == item && r.Placement == placementName {
			return r.Outcome
		}
	}
	return ""
}

// TestTapasUserSync: the machine run skips user-scope placements, stages
// their archives in user-artifacts by hash, and stores the plan from its own
// desired set, so an item pulled in only as a dependency is in it;
// the per-user helper places them in the user's own profile with its own
// receipts, refuses a placement outside the user roots, leaves a held item
// alone, and removes what left the plan.
func TestTapasUserSync(t *testing.T) {
	e := newTapasEnv(t)
	skills := filepath.Join(e.roots, "skills")
	// Slashes, not backslashes, so the paths split on this host too.
	e.servePlacements(map[string]string{
		"skills": skills,
		"codex":  "${USERPROFILE}/.agents/skills",
		"stray":  "${USERPROFILE}/elsewhere/skills",
	}, nil, []string{"codex", "stray"})
	skill := func(body string) map[string]string { return map[string]string{"SKILL.md": body} }
	e.zipItem("both", "1.0", map[string]map[string]string{"skills": skill("both"), "codex": skill("both for codex")})
	e.zipItem("machine-only", "1.0", map[string]map[string]string{"skills": skill("machine only")})
	// dep is user-scope only and desired only as machine-only's dependency.
	e.zipItem("dep", "1.0", map[string]map[string]string{"codex": skill("dep")})
	machineOnly := e.items["machine-only"]
	machineOnly.Dependencies = []string{"dep"}
	e.items["machine-only"] = machineOnly
	e.zipItem("stray", "1.0", map[string]map[string]string{"stray": skill("stray")})
	e.zipItem("held", "1.0", map[string]map[string]string{"codex": skill("held")})
	e.publishCatalog()
	setManifest := func(installs string) {
		e.put("manifests/tapas.yaml", []byte(`name: tapas
catalogs: [tapas]
managed_installs: [`+installs+`]
conditional_items:
  - condition: '"lab" in machine_groups'
    managed_installs: [held]
`))
	}
	setManifest("both, machine-only, stray")
	writeFiles(t, e.userArtifacts, map[string]string{"unreferenced.zip": "old"})

	// The machine run: user placements skipped, their archives staged.
	rep := e.run()
	if !listed(rep.InstalledItems, "both") || failedCodes(rep, "both") != nil || failedCodes(rep, "stray") != nil {
		t.Errorf("machine run: both installed %v, failures %v %v", listed(rep.InstalledItems, "both"), failedCodes(rep, "both"), failedCodes(rep, "stray"))
	}
	if rcs, _ := receipt.List(e.receiptDir()); slices.ContainsFunc(rcs, func(rc receipt.Receipt) bool { return rc.Placement != "skills" }) {
		t.Errorf("machine run wrote user-scope receipts: %+v", rcs)
	}

	plan, err := service.LoadUserPlan(e.cfg.AppDataPath)
	if err != nil {
		t.Fatalf("LoadUserPlan: %v", err)
	}
	got := map[string]service.UserPlacement{}
	for _, entry := range plan.Entries {
		got[entry.Item+"/"+entry.Placement] = entry
	}
	if len(got) != 4 || got["both/codex"].Held || got["dep/codex"].Held || got["stray/stray"].Held || !got["held/codex"].Held {
		t.Fatalf("plan = %+v, want both/codex, dep/codex and stray/stray, and held/codex held", plan)
	}
	// The plan carries the run's facts, for the helper's templates.
	if _, ok := plan.Facts["os"]; !ok {
		t.Errorf("plan facts = %v, want the run's, os included", plan.Facts)
	}
	// What the run stored is what it staged: every non-held entry's archive
	// is at its hash-named path and verifies.
	for _, entry := range plan.Entries {
		if entry.Archive != filepath.Join(e.userArtifacts, entry.Hash+".zip") {
			t.Errorf("%s/%s archive %s is not named by its hash", entry.Item, entry.Placement, entry.Archive)
		}
		if !entry.Held && !download.Verify(entry.Archive, entry.Hash) {
			t.Errorf("%s/%s archive not staged at %s", entry.Item, entry.Placement, entry.Archive)
		}
	}
	both := got["both/codex"]
	if both.Path != "${USERPROFILE}/.agents/skills" || filepath.Dir(both.Archive) != e.userArtifacts || both.Version != "1.0" {
		t.Errorf("both/codex entry = %+v", both)
	}
	if exists(got["held/codex"].Archive) || exists(filepath.Join(e.userArtifacts, "unreferenced.zip")) {
		t.Error("user-artifacts holds a held or unreferenced archive")
	}

	// The helper, as one user. A held tree an earlier sync placed stays.
	profile, userReceipts := tempDir(t), filepath.Join(tempDir(t), "receipts")
	heldTree := writeFiles(t, filepath.Join(profile, ".agents", "skills", "held"), skill("held, older"))
	heldHash, err := artifact.TreeHash(heldTree, tapasOS)
	if err != nil {
		t.Fatal(err)
	}
	heldRc := receipt.Receipt{Item: "held", Version: "0.9", Placement: "codex", Path: heldTree, TreeHash: heldHash, PlacedHash: heldHash, Rendered: []receipt.Rendered{}, InstalledAtUTC: time.Now().UTC().Truncate(time.Second)}
	if err := receipt.Write(userReceipts, heldRc); err != nil {
		t.Fatal(err)
	}
	guard := placement.Guard{
		GOOS:       tapasOS,
		Roots:      placement.Roots{WindowsUser: []string{"${USERPROFILE}/.agents"}},
		DataDir:    e.cfg.AppDataPath,
		BinaryDirs: []string{e.bin},
		Vars:       map[string]string{"USERPROFILE": profile},
	}
	client := &tapasUserClient{cfg: e.cfg}
	sync := func() []service.UserPlacementResult {
		t.Helper()
		client.reported = nil
		results, err := usersync.Sync(context.Background(), client, guard, userReceipts, slog.Default())
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if !slices.Equal(client.reported, results) {
			t.Errorf("reported %+v, want %+v", client.reported, results)
		}
		return results
	}

	results := sync()
	bothTree := filepath.Join(profile, ".agents", "skills", "both")
	if outcomeOf(results, "both", "codex") != string(installer.OutcomeInstalled) || readFile(t, filepath.Join(bothTree, "SKILL.md")) != "both for codex" {
		t.Errorf("both not placed in the profile: %+v", results)
	}
	if rc, ok, err := receipt.Read(userReceipts, "codex", "both"); err != nil || !ok || rc.Path != bothTree || rc.Version != "1.0" || rc.TreeHash != both.TreeHash {
		t.Errorf("both's user receipt = %+v, %v, %v", rc, ok, err)
	}
	if outcomeOf(results, "dep", "codex") != string(installer.OutcomeInstalled) || readFile(t, filepath.Join(profile, ".agents", "skills", "dep", "SKILL.md")) != "dep" {
		t.Errorf("dependency dep not placed in the profile: %+v", results)
	}
	if outcomeOf(results, "stray", "stray") != string(installer.OutcomeRefused) || exists(filepath.Join(profile, "elsewhere")) {
		t.Errorf("stray outside the user roots: %+v", results)
	}
	if rc, _, _ := receipt.Read(userReceipts, "codex", "held"); !reflect.DeepEqual(rc, heldRc) || readFile(t, filepath.Join(heldTree, "SKILL.md")) != "held, older" {
		t.Errorf("held item touched: receipt %+v", rc)
	}
	if outcomeOf(results, "held", "codex") != "" {
		t.Errorf("held item acted on: %+v", results)
	}

	// both leaves the desired set: its staged archive goes, and the next sync
	// removes its tree and receipt. The held item still stays.
	setManifest("machine-only, stray")
	e.run()
	if exists(both.Archive) {
		t.Error("the archive of an item that left the plan is still staged")
	}
	results = sync()
	if outcomeOf(results, "both", "codex") != string(installer.OutcomeRemoved) || exists(bothTree) {
		t.Errorf("both not removed by scope exit: %+v", results)
	}
	if _, ok, _ := receipt.Read(userReceipts, "codex", "both"); ok {
		t.Error("both's user receipt kept")
	}
	if !exists(filepath.Join(heldTree, "SKILL.md")) {
		t.Error("held item removed")
	}
}

// tapasMarketplace is the generated marketplace.json, as far as the tests
// read it.
type tapasMarketplace struct {
	Name    string         `json:"name"`
	Owner   map[string]any `json:"owner"`
	Renames map[string]any `json:"renames"`
	Allow   []any          `json:"allowCrossMarketplaceDependenciesOn"`
	Plugins []struct {
		Name           string `json:"name"`
		Source         string `json:"source"`
		Version        string `json:"version"`
		DefaultEnabled bool   `json:"defaultEnabled"`
	} `json:"plugins"`
}

// tapasDropIn is the generated settings drop-in.
type tapasDropIn struct {
	ExtraKnownMarketplaces map[string]struct {
		Source struct {
			Source string `json:"source"`
			Path   string `json:"path"`
		} `json:"source"`
	} `json:"extraKnownMarketplaces"`
	EnabledPlugins map[string]bool `json:"enabledPlugins"`
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(readFile(t, path)), v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestTapasMarketplace: plugins placed into a marketplace placement, the
// marketplace manifest and settings drop-in regenerated from the receipts
// after each run that changes them. Harnesses enable what the drop-in lists,
// so a managed plugin is enabled, a self-service one only offered, a held
// one keeps the flag its receipt records even when another list desires it,
// and with no plugin left the drop-in goes and the manifest stays valid.
// Every run rewrites the files, so a drop-in refused on one run is written on
// the next with no tree change.
func TestTapasMarketplace(t *testing.T) {
	e := newTapasEnv(t)
	market := filepath.Join(e.roots, "market")
	dropIn := filepath.Join(e.roots, "claude", "managed-settings.d", "50-tapas-test.json")
	setPlacements := func(dropIn string) {
		e.put("placements.yaml", []byte(`placements:
  market:
    kind: marketplace
    paths: {`+tapasOS+`: '`+market+`'}
    exclusive: true
    marketplace:
      name: tapas-test
      owner: {name: Tapas Test}
      renames: {old-plugin: managed-plugin}
      allowCrossMarketplaceDependenciesOn: [other]
  drop-in:
    kind: settings
    paths: {`+tapasOS+`: '`+dropIn+`'}
    registers: market
`))
	}
	// The first run's drop-in path is outside the write roots.
	outside := filepath.Join(tempDir(t), "managed-settings.d", "50-tapas-test.json")
	setPlacements(outside)
	plugin := func(name string) map[string]map[string]string {
		return map[string]map[string]string{"market": {
			".claude-plugin/plugin.json": `{"name": "` + name + `", "version": "1.0.0"}`,
			"README.md":                  name,
		}}
	}
	for _, name := range []string{"managed-plugin", "optional-plugin", "held-plugin"} {
		e.zipItem(name, "1.0.0", plugin(name))
	}
	e.publishCatalog()
	setManifest := func(body string) {
		e.put("manifests/tapas.yaml", []byte("name: tapas\ncatalogs: [tapas]\n"+body))
	}
	setManifest(`managed_installs: [managed-plugin]
optional_installs: [optional-plugin, held-plugin]
conditional_items:
  - condition: '"lab" in machine_groups'
    managed_installs: [held-plugin]
`)
	selfServe := func(entry manifest.Item) {
		if err := manifest.SaveSelfServe(manifest.SelfServePath(e.cfg.AppDataPath), entry); err != nil {
			t.Fatal(err)
		}
	}
	selfServe(manifest.Item{Installs: []string{"held-plugin"}})

	// An earlier run placed held-plugin as managed; its block is now held.
	// The user's selection puts it in the run's install list while Managed
	// reports false, but the run cannot tell, so it keeps the flag.
	heldTree := writeFiles(t, filepath.Join(market, "plugins", "held-plugin"), plugin("held-plugin")["market"])
	heldHash, err := artifact.TreeHash(heldTree, tapasOS)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, market, map[string]string{".tapas-test-owned": ""})
	heldRc := receipt.Receipt{Item: "held-plugin", Version: "1.0.0", Placement: "market", Path: heldTree, TreeHash: heldHash, PlacedHash: heldHash, Rendered: []receipt.Rendered{}, Managed: true, InstalledAtUTC: time.Now().UTC().Truncate(time.Second)}
	if err := receipt.Write(e.receiptDir(), heldRc); err != nil {
		t.Fatal(err)
	}

	type entry struct {
		version string
		enabled bool
	}
	check := func(step string, wantPlugins map[string]entry, wantEnabled []string) {
		t.Helper()
		var m tapasMarketplace
		readJSON(t, filepath.Join(market, ".claude-plugin", "marketplace.json"), &m)
		if m.Name != "tapas-test" || m.Owner["name"] != "Tapas Test" || m.Renames["old-plugin"] != "managed-plugin" ||
			len(m.Allow) != 1 || m.Allow[0] != "other" || m.Plugins == nil {
			t.Errorf("%s: marketplace header = %+v", step, m)
		}
		got := map[string]entry{}
		for _, p := range m.Plugins {
			if p.Source != "./plugins/"+p.Name {
				t.Errorf("%s: %s source = %q", step, p.Name, p.Source)
			}
			got[p.Name] = entry{p.Version, p.DefaultEnabled}
		}
		if !maps.Equal(got, wantPlugins) {
			t.Errorf("%s: plugins = %v, want %v", step, got, wantPlugins)
		}
		if wantEnabled == nil {
			if exists(dropIn) {
				t.Errorf("%s: drop-in kept with no plugin left", step)
			}
			return
		}
		var d tapasDropIn
		readJSON(t, dropIn, &d)
		if src := d.ExtraKnownMarketplaces["tapas-test"].Source; len(d.ExtraKnownMarketplaces) != 1 || src.Source != "directory" || src.Path != market {
			t.Errorf("%s: extraKnownMarketplaces = %+v", step, d.ExtraKnownMarketplaces)
		}
		if got := slices.Sorted(maps.Keys(d.EnabledPlugins)); !slices.Equal(got, wantEnabled) {
			t.Errorf("%s: enabledPlugins = %v, want %v", step, got, wantEnabled)
		}
	}

	// The startup run places managed-plugin, but the drop-in is refused: the
	// run reports it against the marketplace, from the per-run pass.
	rep := e.run()
	if readFile(t, filepath.Join(market, "plugins", "managed-plugin", "README.md")) != "managed-plugin" {
		t.Error("managed-plugin not placed at <root>/plugins/<item>")
	}
	if i := slices.IndexFunc(rep.FailedItems, func(f report.FailedItem) bool { return f.Name == "market" }); i < 0 ||
		rep.FailedItems[i].Action != "generate" || rep.FailedItems[i].Code != report.CodePlacementRefused {
		t.Errorf("refused run: failed %+v, want market generate placement_refused", rep.FailedItems)
	}
	if exists(filepath.Dir(outside)) || exists(dropIn) {
		t.Error("refused drop-in written")
	}

	// The next run, with the drop-in inside the roots, changes no tree but
	// writes it.
	setPlacements(dropIn)
	rep = e.run()
	if len(rep.InstalledItems) != 0 || len(rep.FailedItems) != 0 {
		t.Fatalf("healing run: installed %v, failed %+v", rep.InstalledItems, rep.FailedItems)
	}
	check("startup run",
		map[string]entry{"managed-plugin": {"1.0.0", true}, "held-plugin": {"1.0.0", true}},
		[]string{"held-plugin@tapas-test", "managed-plugin@tapas-test"})
	if rc, _, _ := receipt.Read(e.receiptDir(), "market", "held-plugin"); !reflect.DeepEqual(rc, heldRc) {
		t.Errorf("held receipt changed: %+v", rc)
	}

	// The user selects the optional plugin: offered, not enabled.
	selfServe(manifest.Item{Installs: []string{"held-plugin", "optional-plugin"}})
	e.run()
	check("optional selected",
		map[string]entry{"managed-plugin": {"1.0.0", true}, "held-plugin": {"1.0.0", true}, "optional-plugin": {"1.0.0", false}},
		[]string{"held-plugin@tapas-test", "managed-plugin@tapas-test"})

	// ... and removes it.
	selfServe(manifest.Item{Installs: []string{"held-plugin"}, Uninstalls: []string{"optional-plugin"}})
	e.run()
	if exists(filepath.Join(market, "plugins", "optional-plugin")) {
		t.Error("optional-plugin not removed")
	}
	check("optional removed",
		map[string]entry{"managed-plugin": {"1.0.0", true}, "held-plugin": {"1.0.0", true}},
		[]string{"held-plugin@tapas-test", "managed-plugin@tapas-test"})

	// Every plugin leaves the desired set: scope exit removes them all.
	setManifest("managed_installs: []\n")
	rep = e.run()
	if len(rep.FailedItems) != 0 {
		t.Errorf("last removal failed: %+v", rep.FailedItems)
	}
	check("last plugin removed", map[string]entry{}, nil)
}

// TestTapasRequiresSoftware: a zip item whose required Chocolatey package is
// absent is deferred with missing_software and nothing is written, not even
// the skills root; its dependent waits with it. Once the package directory
// exists, the next run places both.
func TestTapasRequiresSoftware(t *testing.T) {
	e := newTapasEnv(t)
	lib := filepath.Join(tempDir(t), "chocolatey", "lib")
	origOS, origLib := software.GOOS, software.ChocolateyLib
	software.GOOS = tapasOS
	software.ChocolateyLib = func() string { return lib }
	t.Cleanup(func() { software.GOOS, software.ChocolateyLib = origOS, origLib })

	skills := filepath.Join(e.roots, "skills")
	e.servePlacements(map[string]string{"skills": skills}, nil, nil)
	e.zipItem("needs-tool", "1.0", map[string]map[string]string{"skills": {"SKILL.md": "needs tool"}})
	e.zipItem("rider", "1.0", map[string]map[string]string{"skills": {"SKILL.md": "rider"}})
	needs := e.items["needs-tool"]
	needs.RequiresSoftware = []catalog.RequiredSoftware{
		{DisplayName: "Demo Tool", Chocolatey: "demo-tool"},
		{DisplayName: "Mac Tool", Munki: "MacTool"}, // no Windows key: skipped
	}
	e.items["needs-tool"] = needs
	rider := e.items["rider"]
	rider.Dependencies = []string{"needs-tool"}
	e.items["rider"] = rider
	e.publishCatalog()
	e.put("manifests/tapas.yaml", []byte("name: tapas\ncatalogs: [tapas]\nmanaged_installs: [rider]\n"))

	const msg = "Needs Demo Tool. Install it first, then try again."
	rep := e.run()
	for _, name := range []string{"needs-tool", "rider"} {
		i := slices.IndexFunc(rep.DeferredItems, func(d report.DeferredItem) bool { return d.Name == name })
		if i < 0 || rep.DeferredItems[i].Code != report.CodeMissingSoftware || rep.DeferredItems[i].Reason != msg {
			t.Errorf("%s not deferred for missing software: %+v", name, rep.DeferredItems)
		}
	}
	if len(rep.FailedItems) != 0 || len(rep.InstalledItems) != 0 {
		t.Errorf("failed %+v, installed %+v", rep.FailedItems, rep.InstalledItems)
	}
	if exists(skills) {
		t.Error("the skills root was written while Demo Tool is missing")
	}
	if rcs, err := receipt.List(e.receiptDir()); err != nil || len(rcs) != 0 {
		t.Errorf("receipts = %+v, %v", rcs, err)
	}

	if err := os.MkdirAll(filepath.Join(lib, "demo-tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep = e.run()
	if len(rep.DeferredItems) != 0 || len(rep.FailedItems) != 0 {
		t.Errorf("after installing Demo Tool: deferred %+v, failed %+v", rep.DeferredItems, rep.FailedItems)
	}
	for _, name := range []string{"needs-tool", "rider"} {
		if !exists(filepath.Join(skills, name, "SKILL.md")) || !listed(rep.InstalledItems, name) {
			t.Errorf("%s not placed once Demo Tool is installed", name)
		}
	}
}

// TestTapasUserScopeRequiresSoftware: a user-scope-only zip item whose
// required software is absent is deferred by the machine run, and the user
// plan holds it, so its archive is not staged and the helper places nothing.
// Once the software is installed the next run plans, stages and places it.
func TestTapasUserScopeRequiresSoftware(t *testing.T) {
	e := newTapasEnv(t)
	lib := filepath.Join(tempDir(t), "chocolatey", "lib")
	origOS, origLib := software.GOOS, software.ChocolateyLib
	software.GOOS = tapasOS
	software.ChocolateyLib = func() string { return lib }
	t.Cleanup(func() { software.GOOS, software.ChocolateyLib = origOS, origLib })

	e.servePlacements(map[string]string{"codex": "${USERPROFILE}/.agents/skills"}, nil, []string{"codex"})
	e.zipItem("needs-tool", "1.0", map[string]map[string]string{"codex": {"SKILL.md": "needs tool"}})
	needs := e.items["needs-tool"]
	needs.RequiresSoftware = []catalog.RequiredSoftware{{DisplayName: "Demo Tool", Chocolatey: "demo-tool"}}
	e.items["needs-tool"] = needs
	e.publishCatalog()
	e.put("manifests/tapas.yaml", []byte("name: tapas\ncatalogs: [tapas]\nmanaged_installs: [needs-tool]\n"))

	profile, userReceipts := tempDir(t), filepath.Join(tempDir(t), "receipts")
	guard := placement.Guard{
		GOOS:       tapasOS,
		Roots:      placement.Roots{WindowsUser: []string{"${USERPROFILE}/.agents"}},
		DataDir:    e.cfg.AppDataPath,
		BinaryDirs: []string{e.bin},
		Vars:       map[string]string{"USERPROFILE": profile},
	}
	client := &tapasUserClient{cfg: e.cfg}
	tree := filepath.Join(profile, ".agents", "skills", "needs-tool", "SKILL.md")
	planEntry := func() service.UserPlacement {
		t.Helper()
		plan, err := service.LoadUserPlan(e.cfg.AppDataPath)
		if err != nil || len(plan.Entries) != 1 || plan.Entries[0].Item != "needs-tool" {
			t.Fatalf("plan = %+v, %v; want needs-tool/codex only", plan, err)
		}
		return plan.Entries[0]
	}
	sync := func() []service.UserPlacementResult {
		t.Helper()
		results, err := usersync.Sync(context.Background(), client, guard, userReceipts, slog.Default())
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		return results
	}

	rep := e.run()
	i := slices.IndexFunc(rep.DeferredItems, func(d report.DeferredItem) bool { return d.Name == "needs-tool" })
	if i < 0 || rep.DeferredItems[i].Code != report.CodeMissingSoftware {
		t.Errorf("needs-tool not deferred for missing software: %+v", rep.DeferredItems)
	}
	entry := planEntry()
	if !entry.Held || exists(entry.Archive) {
		t.Errorf("deferred entry = %+v, want held and its archive not staged", entry)
	}
	if results := sync(); outcomeOf(results, "needs-tool", "codex") != "" || exists(tree) {
		t.Errorf("helper acted on a deferred item: %+v", results)
	}

	if err := os.MkdirAll(filepath.Join(lib, "demo-tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep = e.run()
	if len(rep.DeferredItems) != 0 || len(rep.FailedItems) != 0 {
		t.Errorf("after installing Demo Tool: deferred %+v, failed %+v", rep.DeferredItems, rep.FailedItems)
	}
	entry = planEntry()
	if entry.Held || !download.Verify(entry.Archive, entry.Hash) {
		t.Errorf("entry = %+v, want planned and staged", entry)
	}
	if results := sync(); outcomeOf(results, "needs-tool", "codex") != string(installer.OutcomeInstalled) || readFile(t, tree) != "needs tool" {
		t.Errorf("helper did not place needs-tool: %+v", results)
	}
}

// tempDir is t.TempDir with its symlinks resolved. On macOS it lives under
// /var/folders, a symlink to /private/var/folders, and the guard compares
// resolved paths.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
