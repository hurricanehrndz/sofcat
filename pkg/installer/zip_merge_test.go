package installer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/jsonmerge"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// mergeTest is an installer in user scope for a profile directory, and a
// machine placement "market" whose scope: user entry adds
// enabledPlugins.<item>@m to the profile's settings.json.
type mergeTest struct {
	z        *ZipInstaller
	spec     placement.Spec
	root     string
	settings string
	record   string
}

func mergeSpec(root, settings string) placement.Spec {
	return placement.Spec{
		Paths: map[string]string{testOS(): root},
		Files: []placement.File{{
			Paths: map[string]string{testOS(): settings}, Scope: placement.ScopeUser, Merge: placement.MergeAddIfAbsent,
			Template: `{"enabledPlugins": { {{- range $i, $p := .Items }}{{ if $i }},{{ end }}{{ json (printf "%s@m" $p.Name) }}: true{{ end -}} }}`,
		}},
	}
}

func newMergeTest(t *testing.T) mergeTest {
	t.Helper()
	base := tempDir(t)
	profile := filepath.Join(base, "home")
	root := filepath.Join(base, "market")
	settings := filepath.Join(profile, ".claude", "settings.json")
	spec := mergeSpec(root, settings)
	return mergeTest{
		z: &ZipInstaller{
			Guard: placement.Guard{
				GOOS: testOS(),
				Roots: placement.Roots{
					Darwin: []string{root}, Windows: []string{root},
					DarwinUser: []string{profile}, WindowsUser: []string{profile},
				},
				Vars: map[string]string{placement.ProfileVar(testOS()): profile},
			},
			Table:       placement.Table{Placements: map[string]placement.Spec{"market": spec}, Order: []string{"market"}},
			ReceiptDir:  filepath.Join(base, "receipts"),
			MergeRecord: filepath.Join(base, "receipts", ".merged-keys.json"),
		},
		spec:     spec,
		root:     root,
		settings: settings,
		record:   filepath.Join(base, "receipts", ".merged-keys.json"),
	}
}

func items(names ...string) []render.Item {
	out := []render.Item{}
	for _, n := range names {
		out = append(out, render.Item{Name: n})
	}
	return out
}

// sync is one helper run for placement "market" holding the named items. A
// new installer each time, as each helper run has.
func (m mergeTest) sync(t *testing.T, names ...string) []Result {
	t.Helper()
	z := *m.z
	z.merged = nil
	if res, ok := z.RegenerateUserFiles("market", m.spec, m.root, items(names...)); !ok {
		return []Result{res}
	}
	return nil
}

func (m mergeTest) mustSync(t *testing.T, names ...string) {
	t.Helper()
	if failed := m.sync(t, names...); len(failed) != 0 {
		t.Fatalf("sync %v = %+v", names, failed)
	}
}

// plugins returns enabledPlugins of the settings file.
func (m mergeTest) plugins(t *testing.T) map[string]bool {
	t.Helper()
	var d struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal([]byte(readFile(t, m.settings)), &d); err != nil {
		t.Fatal(err)
	}
	return d.EnabledPlugins
}

func (m mergeTest) write(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(m.settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recorded returns the record's entries, none when the file is missing.
func (m mergeTest) recorded(t *testing.T) []mergeEntry {
	t.Helper()
	data, err := os.ReadFile(m.record)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var rec mergeRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec.Entries
}

func wantKeys(t *testing.T, entries []mergeEntry, want ...string) {
	t.Helper()
	var got []string
	for _, e := range entries {
		for _, k := range e.Keys {
			got = append(got, strings.Join(k, "/"))
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("recorded keys = %v, want %v", got, want)
	}
}

func TestMergeAddsAndRemovesItsKey(t *testing.T) {
	m := newMergeTest(t)
	m.mustSync(t, "a")
	if got := m.plugins(t); !got["a@m"] || len(got) != 1 {
		t.Errorf("enabledPlugins = %v, want a@m", got)
	}
	wantKeys(t, m.recorded(t), "enabledPlugins/a@m")

	m.mustSync(t)
	if got := m.plugins(t); len(got) != 0 {
		t.Errorf("enabledPlugins = %v after the item left, want none", got)
	}
	wantKeys(t, m.recorded(t))
}

// A key the user had before sofcat is theirs: never changed, recorded or
// removed.
func TestMergeLeavesTheUsersOwnKey(t *testing.T) {
	m := newMergeTest(t)
	m.write(t, `{"enabledPlugins": {"a@m": false}}`)
	m.mustSync(t, "a")
	m.mustSync(t)
	if got := m.plugins(t); got["a@m"] || len(got) != 1 {
		t.Errorf("enabledPlugins = %v, want a@m still false", got)
	}
	wantKeys(t, m.recorded(t))
}

// An owned key goes with its item whatever the user set it to, comes back
// with the item and is re-added when the user deleted it meanwhile.
func TestMergeOwnedKeyLifecycle(t *testing.T) {
	m := newMergeTest(t)
	m.mustSync(t, "a")
	m.write(t, `{"enabledPlugins": {"a@m": false}}`)
	m.mustSync(t, "a")
	if got := m.plugins(t); got["a@m"] {
		t.Errorf("a user's change to an owned key was overridden: %v", got)
	}
	m.mustSync(t)
	if got := m.plugins(t); len(got) != 0 {
		t.Errorf("enabledPlugins = %v, want a@m deleted although the user set it false", got)
	}
	m.mustSync(t, "a")
	if got := m.plugins(t); !got["a@m"] {
		t.Errorf("enabledPlugins = %v, want a@m back as true", got)
	}

	m.write(t, `{"enabledPlugins": {}}`)
	m.mustSync(t, "a")
	if got := m.plugins(t); !got["a@m"] {
		t.Errorf("enabledPlugins = %v, want the deleted owned key re-added", got)
	}
}

func TestMergeCreatesMissingFile(t *testing.T) {
	m := newMergeTest(t)
	m.mustSync(t, "a")
	info, err := os.Stat(m.settings)
	if err != nil {
		t.Fatal(err)
	}
	if osHasModes() && info.Mode().Perm() != 0o644 {
		t.Errorf("new file mode = %v, want 0644", info.Mode().Perm())
	}
	// With nothing to add, a missing file stays missing.
	m2 := newMergeTest(t)
	m2.mustSync(t)
	if exists(m2.settings) || exists(m2.record) {
		t.Error("an empty merge created a file")
	}
}

func TestMergeKeepsFileMode(t *testing.T) {
	if !osHasModes() {
		t.Skip("no permission bits")
	}
	m := newMergeTest(t)
	m.write(t, `{"theme": "dark"}`)
	if err := os.Chmod(m.settings, 0o600); err != nil {
		t.Fatal(err)
	}
	m.mustSync(t, "a")
	info, err := os.Stat(m.settings)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", info.Mode().Perm())
	}
	if !strings.Contains(readFile(t, m.settings), `"theme": "dark"`) {
		t.Errorf("the user's key was lost:\n%s", readFile(t, m.settings))
	}
}

// A document the merge will not touch is refused with the file and the
// record as they were.
func TestMergeRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"invalid JSON":                 `{"enabledPlugins": `,
		"enabledPlugins not an object": `{"enabledPlugins": []}`,
		"not an object":                `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			m := newMergeTest(t)
			m.write(t, body)
			failed := m.sync(t, "a")
			if len(failed) != 1 || !errors.Is(failed[0].Err, placement.ErrRefused) || !errors.Is(failed[0].Err, jsonmerge.ErrRefused) {
				t.Fatalf("sync = %+v, want one refusal", failed)
			}
			if readFile(t, m.settings) != body || exists(m.record) {
				t.Errorf("file = %q, record exists = %v; want both untouched", readFile(t, m.settings), exists(m.record))
			}
		})
	}
}

// The file itself must not be a symlink, checked on the path as named,
// before it is resolved.
func TestMergeRefusesSymlinkedFile(t *testing.T) {
	m := newMergeTest(t)
	target := filepath.Join(filepath.Dir(m.root), "real.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, m.settings); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	failed := m.sync(t, "a")
	if len(failed) != 1 || !errors.Is(failed[0].Err, placement.ErrRefused) {
		t.Fatalf("sync = %+v, want a refusal", failed)
	}
	if readFile(t, target) != `{}` || exists(m.record) {
		t.Errorf("target = %q, record exists = %v; want both untouched", readFile(t, target), exists(m.record))
	}
}

// at points the merge entry at settings instead.
func (m *mergeTest) at(settings string) {
	m.settings = settings
	m.spec = mergeSpec(m.root, settings)
	m.z.Table.Placements["market"] = m.spec
}

// linkDir moves dir to target, which must not exist, and leaves a symlink to
// it at dir; it skips the test where symlinks cannot be made.
func linkDir(t *testing.T, dir, target string) {
	t.Helper()
	if err := os.Rename(dir, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
}

// wantRefusedThroughLink checks a merge refused at the symlink link left
// file, behind it, as before, wrote nothing beside it, and kept the link.
func wantRefusedThroughLink(t *testing.T, failed []Result, link, file, before string) {
	t.Helper()
	if len(failed) != 1 || !errors.Is(failed[0].Err, placement.ErrRefused) {
		t.Fatalf("merge = %+v, want one refusal", failed)
	}
	if got := readFile(t, file); got != before {
		t.Errorf("settings through the link = %q, want %q", got, before)
	}
	if entries, err := os.ReadDir(filepath.Dir(file)); err != nil || len(entries) != 1 {
		t.Errorf("the file's directory holds %v (%v), want only the file", entries, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is no longer a symlink: %v", link, err)
	}
}

// A symlinked directory below the home usually leads somewhere shared, such
// as a dotfiles repository: keys added through it would reach machines with
// no record of them. The merge is refused, with the file and record as they
// were. Every link here leads inside the home, where the guard alone would
// allow the write.
func TestMergeRefusesSymlinkedParent(t *testing.T) {
	m := newMergeTest(t)
	m.write(t, `{"theme": "dark"}`)
	dir := filepath.Dir(m.settings)
	linkDir(t, dir, filepath.Join(filepath.Dir(dir), "dotfiles"))
	wantRefusedThroughLink(t, m.sync(t, "a"), dir, m.settings, `{"theme": "dark"}`)
	if exists(m.record) {
		t.Error("a refused merge wrote the record")
	}
}

func TestMergeRefusesSymlinkTwoLevelsUp(t *testing.T) {
	m := newMergeTest(t)
	home := m.z.Guard.Vars[placement.ProfileVar(testOS())]
	m.at(filepath.Join(home, "a", "b", "settings.json"))
	m.write(t, `{}`)
	linkDir(t, filepath.Join(home, "a"), filepath.Join(home, "elsewhere"))
	wantRefusedThroughLink(t, m.sync(t, "x"), filepath.Join(home, "a"), m.settings, `{}`)
	if exists(m.record) {
		t.Error("a refused merge wrote the record")
	}
}

// The home itself may be a symlink. The record then holds the resolved
// path, which the check still covers when the sweep removes keys.
func TestMergeAllowsSymlinkedHome(t *testing.T) {
	m := newMergeTest(t)
	home := m.z.Guard.Vars[placement.ProfileVar(testOS())]
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(filepath.Dir(m.root), "real-home")
	linkDir(t, home, real)
	m.mustSync(t, "a")
	if !m.plugins(t)["a@m"] {
		t.Fatalf("settings = %s, want a@m", readFile(t, m.settings))
	}
	if e := m.recorded(t); len(e) != 1 || !strings.HasPrefix(e[0].Path, real) {
		t.Fatalf("record = %+v, want the path under %s", e, real)
	}

	dir := filepath.Join(real, ".claude")
	before := readFile(t, m.settings)
	linkDir(t, dir, filepath.Join(real, "dotfiles"))
	z := *m.z
	z.merged = nil
	wantRefusedThroughLink(t, z.SweepMerges(func(string) bool { return false }), dir, m.settings, before)
	wantKeys(t, m.recorded(t), "enabledPlugins/a@m")
}

// Keys added while the directory was real are not removed through a link
// made since, by a merge or by the sweep. The record keeps them, so they go
// once the directory is real again.
func TestMergeKeepsKeysBehindNewSymlink(t *testing.T) {
	m := newMergeTest(t)
	m.mustSync(t, "a")
	before := readFile(t, m.settings)
	dir := filepath.Dir(m.settings)
	moved := filepath.Join(filepath.Dir(dir), "dotfiles")
	linkDir(t, dir, moved)

	wantRefusedThroughLink(t, m.sync(t), dir, m.settings, before)
	z := *m.z
	z.merged = nil
	wantRefusedThroughLink(t, z.SweepMerges(func(string) bool { return false }), dir, m.settings, before)
	wantKeys(t, m.recorded(t), "enabledPlugins/a@m")

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	m.mustSync(t)
	if got := m.plugins(t); len(got) != 0 {
		t.Errorf("enabledPlugins = %v once the directory is real again, want none", got)
	}
	wantKeys(t, m.recorded(t))
}

func TestMergeCorruptRecordRefuses(t *testing.T) {
	m := newMergeTest(t)
	m.mustSync(t, "a")
	before := readFile(t, m.settings)
	if err := os.WriteFile(m.record, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := m.sync(t)
	if len(failed) != 1 || !errors.Is(failed[0].Err, placement.ErrRefused) {
		t.Fatalf("sync = %+v, want a refusal", failed)
	}
	if readFile(t, m.settings) != before || readFile(t, m.record) != "{not json" {
		t.Error("a corrupt record let the merge change a file")
	}
}

func TestMergeWithoutRecordRefuses(t *testing.T) {
	m := newMergeTest(t)
	m.z.MergeRecord = ""
	if failed := m.sync(t, "a"); len(failed) != 1 || !errors.Is(failed[0].Err, placement.ErrRefused) {
		t.Fatalf("sync = %+v, want a refusal", failed)
	}
	if exists(m.settings) {
		t.Error("a merge with no record wrote the file")
	}
}

// The service never writes a user-scope file, however many items are placed.
func TestRegenerateAllSkipsUserScopeFiles(t *testing.T) {
	m := newMergeTest(t)
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		t.Fatal(err)
	}
	m.z.MergeRecord = ""
	if failed := m.z.RegenerateAll(); len(failed) != 0 {
		t.Fatalf("RegenerateAll = %+v", failed)
	}
	if err := m.z.regenerate("market"); err != nil {
		t.Fatal(err)
	}
	if exists(m.settings) {
		t.Error("the service wrote a scope: user file")
	}
}

// Two placements merging one key into one file: the first to run owns it, the
// second leaves it unowned; when the first's item goes the key is removed
// and the second adds and owns it on its turn.
func TestMergeTwoPlacementsOneKey(t *testing.T) {
	m := newMergeTest(t)
	run := func(first, second []render.Item) {
		z := *m.z
		z.merged = nil
		if res, ok := z.RegenerateUserFiles("p1", m.spec, m.root, first); !ok {
			t.Fatal(res)
		}
		if res, ok := z.RegenerateUserFiles("p2", m.spec, m.root, second); !ok {
			t.Fatal(res)
		}
	}
	owner := func() string {
		var names []string
		for _, e := range m.recorded(t) {
			names = append(names, e.Placement)
		}
		return strings.Join(names, ",")
	}
	run(items("a"), items("a"))
	if owner() != "p1" || !m.plugins(t)["a@m"] {
		t.Fatalf("owners = %q, plugins = %v; want p1 to own a@m", owner(), m.plugins(t))
	}
	run(nil, items("a"))
	if owner() != "p2" || !m.plugins(t)["a@m"] {
		t.Errorf("owners = %q, plugins = %v; want p2 to own a@m after p1's item left", owner(), m.plugins(t))
	}
	run(nil, nil)
	if owner() != "" || len(m.plugins(t)) != 0 {
		t.Errorf("owners = %q, plugins = %v; want none", owner(), m.plugins(t))
	}
}

// SweepMerges takes back the keys of an entry nothing wrote this run, keeps
// those it just wrote or was told to skip, and keeps an entry it was refused.
func TestSweepMerges(t *testing.T) {
	m := newMergeTest(t)
	m.mustSync(t, "a")

	z := *m.z
	z.merged = nil
	if failed := z.SweepMerges(func(string) bool { return true }); len(failed) != 0 || !m.plugins(t)["a@m"] {
		t.Errorf("a skipped placement was swept: %+v, %v", failed, m.plugins(t))
	}
	if res, ok := z.RegenerateUserFiles("market", m.spec, m.root, items("a")); !ok {
		t.Fatal(res)
	}
	if failed := z.SweepMerges(func(string) bool { return false }); len(failed) != 0 || !m.plugins(t)["a@m"] {
		t.Errorf("an entry written this run was swept: %+v, %v", failed, m.plugins(t))
	}

	m.write(t, `{"enabledPlugins": `)
	z2 := *m.z
	z2.merged = nil
	if failed := z2.SweepMerges(func(string) bool { return false }); len(failed) != 1 {
		t.Fatalf("sweeping an invalid file = %+v, want one failure", failed)
	}
	wantKeys(t, m.recorded(t), "enabledPlugins/a@m")

	m.write(t, `{"enabledPlugins": {"a@m": true, "b": true}}`)
	if failed := z2.SweepMerges(func(string) bool { return false }); len(failed) != 0 {
		t.Fatal(failed)
	}
	if got := m.plugins(t); got["a@m"] || !got["b"] {
		t.Errorf("enabledPlugins = %v, want only the user's b", got)
	}
	wantKeys(t, m.recorded(t))
}

// A record that cannot be saved stops the merge before the file is written:
// a key in the file that no record owns would never be taken back.
func TestMergeRecordSavedBeforeFile(t *testing.T) {
	if !osHasModes() || os.Geteuid() == 0 {
		t.Skip("needs permission bits and a non-root user")
	}
	m := newMergeTest(t)
	dir := filepath.Dir(m.record)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if failed := m.sync(t, "a"); len(failed) != 1 {
		t.Fatalf("sync = %+v, want one failure", failed)
	}
	if exists(m.settings) {
		t.Error("the file was written although the record could not be saved")
	}
}

// The record's path matches the leaf whatever its case, as claim does.
func TestMergeRecordPathIgnoresCase(t *testing.T) {
	m := newMergeTest(t)
	m.mustSync(t, "a")
	entries := m.recorded(t)
	entries[0].Path = strings.ToUpper(entries[0].Path)
	data, err := json.Marshal(mergeRecord{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.record, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (mergeRecord{Entries: entries}).owned("market", m.settings); len(got) != 1 {
		t.Errorf("owned = %v, want the upper-cased entry matched", got)
	}
	// The same installer just merged into it, so a sweep leaves it alone.
	z := *m.z
	z.merged = nil
	if res, ok := z.RegenerateUserFiles("market", m.spec, m.root, items("a")); !ok {
		t.Fatal(res)
	}
	if failed := z.SweepMerges(func(string) bool { return false }); len(failed) != 0 || !m.plugins(t)["a@m"] {
		t.Errorf("sweep = %+v, plugins = %v; want a@m kept", failed, m.plugins(t))
	}
	wantKeys(t, m.recorded(t), "enabledPlugins/a@m")
}

// A user-scope placement's own merge entry goes through Remove: with its last
// item gone the key is taken back.
func TestMergeUserPlacementRemove(t *testing.T) {
	m := newMergeTest(t)
	skills := filepath.Join(filepath.Dir(m.settings), "skills")
	spec := mergeSpec(skills, m.settings)
	spec.Scope = map[string]string{testOS(): placement.ScopeUser}
	m.z.Table.Placements["up"] = spec
	rc := receipt.Receipt{Item: "a", Version: "1", Placement: "up", Path: filepath.Join(skills, "a"), TreeHash: "t", PlacedHash: "t"}
	if err := receipt.Write(m.z.ReceiptDir, rc); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if failed, _ := m.z.Regenerate([]string{"up"}); len(failed) != 0 {
		t.Fatal(failed)
	}
	if !m.plugins(t)["a@m"] {
		t.Fatalf("settings = %s, want a@m", readFile(t, m.settings))
	}
	if res := m.z.Remove(rc); res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := m.plugins(t); len(got) != 0 {
		t.Errorf("enabledPlugins = %v after Remove, want none", got)
	}
	wantKeys(t, m.recorded(t))
}
