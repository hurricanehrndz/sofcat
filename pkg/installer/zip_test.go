package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// testOS is the placement OS the tests place for: the host's, with POSIX hosts
// standing in for macOS.
func testOS() string {
	if runtime.GOOS == "windows" {
		return placement.Windows
	}
	return placement.Darwin
}

func osHasModes() bool { return runtime.GOOS != "windows" }

// writeTree writes files under a new temp dir; names in exec get 0755.
func writeTree(t *testing.T, files map[string]string, exec ...string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range exec {
		if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func treeHashOf(t *testing.T, files map[string]string, exec ...string) string {
	t.Helper()
	h, err := artifact.TreeHash(writeTree(t, files, exec...), testOS())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// packVersion packs files as version of item "hello" for the "skills"
// placement and returns the item and the archive path.
func packVersion(t *testing.T, version string, files map[string]string) (catalog.Item, string) {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "hello-"+version+".zip")
	packed, err := artifact.PackFile(writeTree(t, files), testOS(), archive)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.Item{
		Name:    "hello",
		Version: version,
		Installer: catalog.InstallerItem{Type: "zip", Variants: map[string]map[string]catalog.Variant{
			"skills":    {testOS(): {Location: "hello.zip", Hash: packed.Hash, TreeHash: packed.TreeHash}},
			"exclusive": {testOS(): {Location: "hello.zip", Hash: packed.Hash, TreeHash: packed.TreeHash}},
		}},
	}, archive
}

// newTestZip returns an installer whose single write root is a temp dir
// holding two placements: skills, and the exclusive one.
func newTestZip(t *testing.T) (*ZipInstaller, string) {
	t.Helper()
	base := tempDir(t)
	roots := filepath.Join(base, "roots")
	paths := func(dir string) map[string]string {
		p := filepath.Join(roots, dir)
		return map[string]string{placement.Darwin: p, placement.Windows: p}
	}
	table := placement.Table{
		Placements: map[string]placement.Spec{
			"skills":    {Kind: placement.KindDirectory, Paths: paths("skills")},
			"exclusive": {Kind: placement.KindDirectory, Paths: paths("exclusive"), Exclusive: true},
		},
		Order: []string{"skills", "exclusive"},
	}
	return &ZipInstaller{
		Guard: placement.Guard{
			GOOS:    testOS(),
			Roots:   placement.Roots{Darwin: []string{roots}, Windows: []string{roots}},
			DataDir: filepath.Join(base, "data"),
		},
		Table:      table,
		ReceiptDir: filepath.Join(base, "data", "receipts"),
		Slug:       "sofcat-test",
	}, roots
}

func target(t *testing.T, z *ZipInstaller, item catalog.Item, name string) Target {
	t.Helper()
	machine, _ := z.Targets(item)
	for _, tg := range machine {
		if tg.Placement == name {
			return tg
		}
	}
	t.Fatalf("no target %s", name)
	return Target{}
}

func wantOutcome(t *testing.T, step string, got Result, want Outcome) {
	t.Helper()
	if got.Outcome != want {
		t.Fatalf("%s: outcome = %s (err %v), want %s", step, got.Outcome, got.Err, want)
	}
}

// TestZipStateOrder walks one placement through R14's order (missing,
// catalog-equal, receipt-equal, conflict) and R15's removal rules, because the
// order is what keeps a user's edits from being overwritten or deleted.
func TestZipStateOrder(t *testing.T) {
	z, roots := newTestZip(t)
	v1Files := map[string]string{"SKILL.md": "# v1\n"}
	v1, v1Archive := packVersion(t, "1.0", v1Files)
	tree := filepath.Join(roots, "skills", "hello")
	tg := target(t, z, v1, "skills")

	// 1. Missing: install.
	wantOutcome(t, "inspect empty", z.Inspect(v1, tg), OutcomeMissing)
	wantOutcome(t, "install", z.Apply(v1, tg, v1Archive), OutcomeInstalled)
	rc, ok, err := receipt.Read(z.ReceiptDir, "skills", "hello")
	if err != nil || !ok || rc.TreeHash != tg.Variant.TreeHash || rc.Version != "1.0" || rc.Path != tree {
		t.Fatalf("receipt after install = %+v, %v, %v", rc, ok, err)
	}
	// 2. Catalog-equal with an up-to-date receipt: current, nothing written.
	wantOutcome(t, "inspect placed", z.Inspect(v1, tg), OutcomeCurrent)
	wantOutcome(t, "apply placed", z.Apply(v1, tg, ""), OutcomeCurrent)

	// 2b. Catalog-equal without a receipt: adopted, receipt written, tree kept.
	if err := receipt.Delete(z.ReceiptDir, "skills", "hello"); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "inspect unreceipted", z.Inspect(v1, tg), OutcomeAdopted)
	wantOutcome(t, "adopt", z.Apply(v1, tg, ""), OutcomeAdopted)
	if _, ok, _ := receipt.Read(z.ReceiptDir, "skills", "hello"); !ok {
		t.Fatal("adoption wrote no receipt")
	}

	// 3. Receipt-equal, catalog moved on: unchanged, replaced by the new version.
	v2, v2Archive := packVersion(t, "2.0", map[string]string{"SKILL.md": "# v2\n", "extra.md": "x"})
	tg2 := target(t, z, v2, "skills")
	wantOutcome(t, "inspect old version", z.Inspect(v2, tg2), OutcomeUnchanged)
	wantOutcome(t, "upgrade", z.Apply(v2, tg2, v2Archive), OutcomeInstalled)
	if got, _ := os.ReadFile(filepath.Join(tree, "SKILL.md")); string(got) != "# v2\n" {
		t.Fatalf("upgraded SKILL.md = %q", got)
	}
	entries, _ := os.ReadDir(filepath.Join(roots, "skills"))
	if len(entries) != 1 {
		t.Fatalf("placement root holds %d entries after the upgrade, want only the tree", len(entries))
	}

	// 4. User edit: neither catalog- nor receipt-equal. Conflict, untouched.
	edited := filepath.Join(tree, "SKILL.md")
	if err := os.WriteFile(edited, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v3, v3Archive := packVersion(t, "3.0", map[string]string{"SKILL.md": "# v3\n"})
	res := z.Apply(v3, target(t, z, v3, "skills"), v3Archive)
	wantOutcome(t, "upgrade over an edit", res, OutcomeConflict)
	if res.Code() != report.CodePlacementConflict {
		t.Errorf("conflict code = %q", res.Code())
	}
	if got, _ := os.ReadFile(edited); string(got) != "# mine\n" {
		t.Fatalf("conflict overwrote the user's edit: %q", got)
	}

	// R15: an edited tree is not removed either.
	rc, _, _ = receipt.Read(z.ReceiptDir, "skills", "hello")
	wantOutcome(t, "remove an edited tree", z.Remove(rc), OutcomeConflict)
	if _, err := os.Stat(edited); err != nil {
		t.Fatal("removal deleted an edited tree")
	}

	// Undo the edit: now it matches its receipt and is removed.
	if err := os.WriteFile(edited, []byte("# v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "remove", z.Remove(rc), OutcomeRemoved)
	assertAbsent(t, tree)
	if _, ok, _ := receipt.Read(z.ReceiptDir, "skills", "hello"); ok {
		t.Fatal("receipt kept after removal")
	}

	// A receipt whose tree is already gone is just dropped.
	if err := receipt.Write(z.ReceiptDir, rc); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "remove a vanished tree", z.Remove(rc), OutcomeRemoved)

	// A tree with no receipt that is not the catalog's is a user's: conflict.
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "notes.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "unreceipted foreign tree", z.Inspect(v1, tg), OutcomeConflict)
}

// TestZipExclusive: R9. An exclusive root that holds someone else's files is
// refused; one sofcat creates is marked as sofcat's, so later runs proceed.
func TestZipExclusive(t *testing.T) {
	z, roots := newTestZip(t)
	item, archive := packVersion(t, "1.0", map[string]string{"SKILL.md": "x"})
	tg := target(t, z, item, "exclusive")
	root := filepath.Join(roots, "exclusive")

	// Created by sofcat: marked.
	wantOutcome(t, "fresh root", z.Apply(item, tg, archive), OutcomeInstalled)
	if _, err := os.Stat(filepath.Join(root, ".sofcat-test-owned")); err != nil {
		t.Fatalf("created root has no ownership marker: %v", err)
	}
	wantOutcome(t, "marked root", z.Inspect(item, tg), OutcomeCurrent)

	// Someone else's root: refused, nothing written.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "theirs"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := z.Apply(item, tg, archive)
	wantOutcome(t, "foreign root", res, OutcomeRefused)
	if res.Code() != report.CodePlacementRefused || !errors.Is(res.Err, placement.ErrRefused) {
		t.Errorf("refusal = %q, %v", res.Code(), res.Err)
	}
	assertAbsent(t, filepath.Join(root, "hello"))
}

// TestZipTargets: placements come in table order, user-scope ones are
// set apart for a machine run (R43a), and a placement the table lacks is
// refused rather than guessed.
func TestZipTargets(t *testing.T) {
	z, _ := newTestZip(t)
	userSpec := placement.Spec{Kind: placement.KindDirectory, Paths: map[string]string{testOS(): "/u"}, Scope: map[string]string{testOS(): placement.ScopeUser}}
	z.Table.Placements["user"] = userSpec
	z.Table.Order = append([]string{"user"}, z.Table.Order...)
	item, _ := packVersion(t, "1.0", map[string]string{"a": "b"})
	v := item.Installer.Variants["skills"]
	item.Installer.Variants["user"] = v
	item.Installer.Variants["nowhere"] = v
	item.Installer.Variants["other-os"] = map[string]catalog.Variant{"plan9": {}}

	machine, user := z.Targets(item)
	var names []string
	for _, tg := range machine {
		names = append(names, tg.Placement)
	}
	if want := []string{"skills", "exclusive", "nowhere"}; !slices.Equal(names, want) {
		t.Errorf("machine targets = %v, want %v", names, want)
	}
	if !slices.Equal(user, []string{"user"}) {
		t.Errorf("user targets = %v", user)
	}
	res := z.Inspect(item, machine[2])
	if res.Outcome != OutcomeRefused {
		t.Errorf("unknown placement = %s, want refused", res.Outcome)
	}
}

// TestZipRefusesHostileItemName: the item name becomes a directory under the
// root, so it must be one safe segment.
func TestZipRefusesHostileItemName(t *testing.T) {
	z, _ := newTestZip(t)
	item, archive := packVersion(t, "1.0", map[string]string{"a": "b"})
	tg := target(t, z, item, "skills")
	for _, name := range []string{"..", "../x", ".hidden", "a:b"} {
		item.Name = name
		if res := z.Apply(item, tg, archive); res.Outcome != OutcomeRefused {
			t.Errorf("item %q: outcome %s", name, res.Outcome)
		}
	}
}

// TestZipFetchRefusesHostileLocation: the location names the cached copy, so
// one that would leave the cache, or name a file Windows cannot hold, is
// refused before anything is downloaded or written.
func TestZipFetchRefusesHostileLocation(t *testing.T) {
	cache := t.TempDir()
	z := &ZipInstaller{CachePath: filepath.Join(cache, "cache"), URLPackages: "http://127.0.0.1:1/"}
	for _, location := range []string{`..\..\x.zip`, "a/../../x.zip", "CON.zip", "packages/CON.zip", ""} {
		got, err := z.Fetch(context.Background(), Target{Placement: "skills", Variant: catalog.Variant{Location: location, Hash: "00"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("location %q: Fetch = %q, %v; want refused", location, got, err)
		}
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Errorf("Fetch wrote %v", entries)
	}
}

// TestZipInstalled: an item is installed while sofcat tracks a tree for every
// placement, edited or not, so a pending removal that met a user's edit is not
// dropped as done while the tree and its receipt remain.
func TestZipInstalled(t *testing.T) {
	z, roots := newTestZip(t)
	item, archive := packVersion(t, "1.0", map[string]string{"SKILL.md": "v1"})
	if z.Installed(item) {
		t.Fatal("nothing placed, yet installed")
	}
	wantOutcome(t, "install skills", z.Apply(item, target(t, z, item, "skills"), archive), OutcomeInstalled)
	if z.Installed(item) {
		t.Fatal("one of two placements placed, yet installed")
	}
	wantOutcome(t, "install exclusive", z.Apply(item, target(t, z, item, "exclusive"), archive), OutcomeInstalled)
	if !z.Installed(item) {
		t.Fatal("every placement placed, yet not installed")
	}

	// A conflict: the user edited the tree. It still has its receipt.
	tree := filepath.Join(roots, "skills", "hello")
	if err := os.WriteFile(filepath.Join(tree, "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "inspect edited", z.Inspect(item, target(t, z, item, "skills")), OutcomeConflict)
	if !z.Installed(item) {
		t.Error("an edited tree with a receipt reads as not installed")
	}

	// A receipt whose tree is gone is not installed.
	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	if z.Installed(item) {
		t.Error("a receipt without its tree reads as installed")
	}
	// A catalog-equal tree sofcat does not track is not installed either.
	wantOutcome(t, "reinstall", z.Apply(item, target(t, z, item, "skills"), archive), OutcomeInstalled)
	if err := receipt.Delete(z.ReceiptDir, "skills", "hello"); err != nil {
		t.Fatal(err)
	}
	if z.Installed(item) {
		t.Error("an untracked tree reads as installed")
	}
}

// TestZipManagedFlagRefresh: moving an item between optional and managed
// installs leaves its tree current, but the receipt's managed flag must follow
// so scope exit and inventory see the item's new standing.
func TestZipManagedFlagRefresh(t *testing.T) {
	z, roots := newTestZip(t)
	item, archive := packVersion(t, "1.0", map[string]string{"SKILL.md": "v1"})
	tg := target(t, z, item, "skills")
	wantOutcome(t, "install as optional", z.Apply(item, tg, archive), OutcomeInstalled)
	if rc, _, _ := receipt.Read(z.ReceiptDir, "skills", "hello"); rc.Managed {
		t.Fatal("optional install flagged managed")
	}
	// Without a Managed func the receipt is up to date.
	wantOutcome(t, "inspect without Managed", z.Inspect(item, tg), OutcomeCurrent)

	sentinel := filepath.Join(roots, "skills", "hello", "SKILL.md")
	before, err := os.Stat(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	z.Managed = func(string) bool { return true }
	wantOutcome(t, "apply now managed", z.Apply(item, tg, ""), OutcomeAdopted)
	rc, _, _ := receipt.Read(z.ReceiptDir, "skills", "hello")
	if !rc.Managed {
		t.Error("receipt's managed flag not refreshed")
	}
	if after, err := os.Stat(sentinel); err != nil || !os.SameFile(before, after) {
		t.Errorf("refreshing the flag touched the tree: %v", err)
	}
	wantOutcome(t, "inspect refreshed", z.Inspect(item, tg), OutcomeCurrent)
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
