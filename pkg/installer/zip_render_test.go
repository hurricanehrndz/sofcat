package installer

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
)

// noTemplates renders nothing, so a tree is placed as shipped and every
// receipt's inputs match: the installer before tree templates existed.
type noTemplates struct{}

func (noTemplates) render(catalog.Item, Target, string, string, bool) ([]rendered, error) {
	return nil, nil
}

func (noTemplates) inputsMatch(catalog.Item, Target, string, string, receipt.Receipt, bool) (bool, error) {
	return true, nil
}

// fakeTemplates exercises the seam apart from the real renderer: it renders
// every .tmpl in the tree to the path without the suffix, as the template's
// bytes followed by the live inputs, and records those inputs.
type fakeTemplates struct {
	// live returns what a render records as inputs for managed.
	live func(managed bool) any
	// outputs overrides a template's output path.
	outputs map[string]string
}

func (f *fakeTemplates) render(_ catalog.Item, _ Target, _, tree string, managed bool) ([]rendered, error) {
	tr, err := artifact.Read(tree, testOS())
	if err != nil {
		return nil, err
	}
	inputs, err := json.Marshal(f.live(managed))
	if err != nil {
		return nil, err
	}
	var outs []rendered
	for _, p := range tr.Paths() {
		if !strings.HasSuffix(p, artifact.TemplateSuffix) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		out := strings.TrimSuffix(p, artifact.TemplateSuffix)
		if o, ok := f.outputs[p]; ok {
			out = o
		}
		outs = append(outs, rendered{Rendered: receipt.Rendered{File: out, Template: p, Inputs: inputs}, data: slices.Concat(src, inputs)})
	}
	return outs, nil
}

func (f *fakeTemplates) inputsMatch(_ catalog.Item, _ Target, _, tree string, rc receipt.Receipt, managed bool) (bool, error) {
	if ok, err := templatesRecorded(tree, testOS(), rc.Rendered); !ok || err != nil {
		return ok, err
	}
	for _, r := range rc.Rendered {
		if eq, err := inputsEqual(r.Inputs, f.live(managed)); !eq || err != nil {
			return eq, err
		}
	}
	return true, nil
}

// liveInputs is a render's inputs for user: a string, a bool, an int and a
// string list, the shapes that must survive the receipt's JSON.
func liveInputs(user string) func(bool) any {
	return func(managed bool) any {
		return map[string]any{"user": user, "item": map[string]any{"managed": managed}, "n": 3, "list": []string{"a", "b"}}
	}
}

// renderedBody is what fakeTemplates writes for template body with inputs.
func renderedBody(t *testing.T, body string, live func(bool) any, managed bool) string {
	t.Helper()
	inputs, err := json.Marshal(live(managed))
	if err != nil {
		t.Fatal(err)
	}
	return body + string(inputs)
}

// templated is a tree with two templates, one in a subdirectory.
var templated = map[string]string{"SKILL.md": "# v1\n", "a.tmpl": "A ", "sub/b.tmpl": "B ", "sub/c.md": "c"}

func readReceipt(t *testing.T, z *ZipInstaller) receipt.Receipt {
	t.Helper()
	rc, ok, err := receipt.Read(z.ReceiptDir, "skills", "hello")
	if err != nil || !ok {
		t.Fatalf("receipt = %v, %v", ok, err)
	}
	return rc
}

// wantPlaced checks that the tree on disk is what rc says sofcat wrote, with
// n rendered records.
func wantPlaced(t *testing.T, step, tree string, rc receipt.Receipt, n int) {
	t.Helper()
	hash, err := artifact.TreeHash(tree, testOS())
	if err != nil || hash != rc.PlacedHash {
		t.Fatalf("%s: tree hash %s, %v; receipt placed_hash %s", step, hash, err, rc.PlacedHash)
	}
	if len(rc.Rendered) != n {
		t.Fatalf("%s: %d rendered records, want %d: %+v", step, len(rc.Rendered), n, rc.Rendered)
	}
	if n > 0 && rc.PlacedHash == rc.TreeHash {
		t.Fatalf("%s: placed_hash equals tree_hash with %d files rendered", step, n)
	}
}

// TestZipRenderedTree: a tree with templates is rendered in staging and its
// receipt records the hash of what was written; a change to an input
// re-renders in place without the archive and leaves every other file alone;
// a list call without a Managed func treats the flag as unchanged; a user
// edit to a rendered file is a conflict.
func TestZipRenderedTree(t *testing.T) {
	z, roots := newTestZip(t)
	fake := &fakeTemplates{live: liveInputs("alice")}
	z.renderer = fake
	z.Managed = func(string) bool { return true }
	item, archive := packVersion(t, "1.0", templated)
	tg := target(t, z, item, "skills")
	tree := filepath.Join(roots, "skills", "hello")
	rendered := func(name string) string { return readFile(t, filepath.Join(tree, filepath.FromSlash(name))) }

	wantOutcome(t, "install", z.Apply(item, tg, archive), OutcomeInstalled)
	rc := readReceipt(t, z)
	wantPlaced(t, "install", tree, rc, 2)
	if rc.TreeHash != tg.Variant.TreeHash || rc.Rendered[0].File != "a" || rc.Rendered[0].Template != "a.tmpl" || rc.Rendered[1].File != "sub/b" {
		t.Fatalf("receipt = %+v", rc)
	}
	if got, want := rendered("a"), renderedBody(t, "A ", fake.live, true); got != want {
		t.Fatalf("rendered a = %q, want %q", got, want)
	}
	// The inputs round-tripped through the receipt (an int, a list) still
	// compare equal to the live ones.
	wantOutcome(t, "inspect rendered", z.Inspect(item, tg), OutcomeCurrent)
	wantOutcome(t, "apply rendered", z.Apply(item, tg, ""), OutcomeCurrent)
	if readReceipt(t, z).InstalledAtUTC != rc.InstalledAtUTC {
		t.Fatal("a run with nothing changed rewrote the receipt")
	}

	// The list call has no Managed func: the flag counts as unchanged.
	z.Managed = nil
	wantOutcome(t, "inspect without Managed", z.Inspect(item, tg), OutcomeCurrent)
	z.Managed = func(string) bool { return true }

	// The user changes: re-rendered in place, from the templates in the tree.
	fake.live = liveInputs("bob")
	wantOutcome(t, "inspect changed inputs", z.Inspect(item, tg), OutcomeAdopted)
	skill := filepath.Join(tree, "SKILL.md")
	before, err := os.Stat(skill)
	if err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "re-render", z.Apply(item, tg, ""), OutcomeAdopted)
	rc2 := readReceipt(t, z)
	wantPlaced(t, "re-render", tree, rc2, 2)
	if rc2.PlacedHash == rc.PlacedHash || rc2.TreeHash != rc.TreeHash || !strings.Contains(string(rc2.Rendered[1].Inputs), "bob") {
		t.Fatalf("re-rendered receipt = %+v, was %+v", rc2, rc)
	}
	if got, want := rendered("sub/b"), renderedBody(t, "B ", fake.live, true); got != want {
		t.Fatalf("re-rendered sub/b = %q, want %q", got, want)
	}
	if after, err := os.Stat(skill); err != nil || !os.SameFile(before, after) {
		t.Errorf("re-rendering touched SKILL.md: %v", err)
	}
	wantOutcome(t, "inspect re-rendered", z.Inspect(item, tg), OutcomeCurrent)

	// A managed flip is an input too.
	z.Managed = func(string) bool { return false }
	wantOutcome(t, "inspect flipped", z.Inspect(item, tg), OutcomeAdopted)
	wantOutcome(t, "apply flipped", z.Apply(item, tg, ""), OutcomeAdopted)
	if rc3 := readReceipt(t, z); rc3.Managed || rendered("a") != renderedBody(t, "A ", fake.live, false) {
		t.Fatalf("flip: receipt %+v, a = %q", rc3, rendered("a"))
	}

	// The catalog moves on: the tree is sofcat's, so it is replaced, and the
	// new version is rendered in staging too.
	v2, v2Archive := packVersion(t, "2.0", map[string]string{"SKILL.md": "# v2\n", "a.tmpl": "A2 "})
	tg2 := target(t, z, v2, "skills")
	wantOutcome(t, "inspect old version", z.Inspect(v2, tg2), OutcomeUnchanged)
	wantOutcome(t, "upgrade", z.Apply(v2, tg2, v2Archive), OutcomeInstalled)
	rc4 := readReceipt(t, z)
	wantPlaced(t, "upgrade", tree, rc4, 1)
	if rc4.Version != "2.0" || rendered("a") != renderedBody(t, "A2 ", fake.live, false) {
		t.Fatalf("upgrade: receipt %+v, a = %q", rc4, rendered("a"))
	}

	// A user edit to a rendered file: neither hash matches. Left alone.
	edited := filepath.Join(tree, "a")
	if err := os.WriteFile(edited, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "inspect edited output", z.Inspect(v2, tg2), OutcomeConflict)
	wantOutcome(t, "remove edited output", z.Remove(rc4), OutcomeConflict)
	if readFile(t, edited) != "mine" {
		t.Fatal("the edited output was overwritten")
	}
	if err := os.WriteFile(edited, []byte(renderedBody(t, "A2 ", fake.live, false)), 0o644); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "remove", z.Remove(rc4), OutcomeRemoved)
	assertAbsent(t, tree)
}

// TestZipAdoptsUnrenderedTree: a tree whose templates nothing has rendered
// (placed before rendering existed, or by hand as the catalog ships it) is
// adopted and rendered in place, without the archive.
func TestZipAdoptsUnrenderedTree(t *testing.T) {
	z, roots := newTestZip(t)
	item, archive := packVersion(t, "1.0", templated)
	tg := target(t, z, item, "skills")
	tree := filepath.Join(roots, "skills", "hello")

	// Placed as shipped by an installer from before tree templates, which
	// rendered nothing; the receipt says so.
	z.renderer = noTemplates{}
	wantOutcome(t, "install as shipped", z.Apply(item, tg, archive), OutcomeInstalled)
	rc := readReceipt(t, z)
	wantPlaced(t, "install as shipped", tree, rc, 0)
	if rc.PlacedHash != rc.TreeHash {
		t.Fatalf("receipt = %+v", rc)
	}
	wantOutcome(t, "inspect as shipped", z.Inspect(item, tg), OutcomeCurrent)

	// Now sofcat renders: a .tmpl without a record counts as changed.
	fake := &fakeTemplates{live: liveInputs("alice")}
	z.renderer = fake
	wantOutcome(t, "inspect unrecorded templates", z.Inspect(item, tg), OutcomeAdopted)
	wantOutcome(t, "render in place", z.Apply(item, tg, ""), OutcomeAdopted)
	rc = readReceipt(t, z)
	wantPlaced(t, "render in place", tree, rc, 2)
	if readFile(t, filepath.Join(tree, "a")) != renderedBody(t, "A ", fake.live, false) {
		t.Fatal("a not rendered in place")
	}
	wantOutcome(t, "inspect rendered", z.Inspect(item, tg), OutcomeCurrent)

	// A catalog-equal tree placed by hand, with no receipt.
	if err := os.RemoveAll(tree); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Delete(z.ReceiptDir, "skills", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(tree, os.DirFS(writeTree(t, templated))); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "inspect hand-placed", z.Inspect(item, tg), OutcomeAdopted)
	wantOutcome(t, "adopt hand-placed", z.Apply(item, tg, ""), OutcomeAdopted)
	wantPlaced(t, "adopt hand-placed", tree, readReceipt(t, z), 2)
	wantOutcome(t, "inspect adopted", z.Inspect(item, tg), OutcomeCurrent)
}

// TestZipLegacyReceipt: a receipt from before placed_hash existed is read as
// if placed_hash were its tree_hash, so the tree it records stays sofcat's to
// keep and to remove.
func TestZipLegacyReceipt(t *testing.T) {
	z, roots := newTestZip(t)
	item, archive := packVersion(t, "1.0", map[string]string{"SKILL.md": "v1"})
	tg := target(t, z, item, "skills")
	wantOutcome(t, "install", z.Apply(item, tg, archive), OutcomeInstalled)

	path := filepath.Join(z.ReceiptDir, "skills", "hello.json")
	var fields map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "placed_hash")
	delete(fields, "rendered")
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	rc := readReceipt(t, z)
	if rc.PlacedHash != rc.TreeHash || len(rc.Rendered) != 0 {
		t.Fatalf("legacy receipt read as %+v", rc)
	}
	wantOutcome(t, "inspect legacy", z.Inspect(item, tg), OutcomeCurrent)
	wantOutcome(t, "remove legacy", z.Remove(rc), OutcomeRemoved)
	assertAbsent(t, filepath.Join(roots, "skills", "hello"))
}

// TestZipRerenderRestores: when the second file of an in-place re-render
// cannot be written, the first gets its bytes back, so the tree still
// matches its receipt and the next run retries.
func TestZipRerenderRestores(t *testing.T) {
	z, roots := newTestZip(t)
	fake := &fakeTemplates{live: liveInputs("alice")}
	z.renderer = fake
	item, archive := packVersion(t, "1.0", templated)
	tg := target(t, z, item, "skills")
	tree := filepath.Join(roots, "skills", "hello")
	wantOutcome(t, "install", z.Apply(item, tg, archive), OutcomeInstalled)
	rc := readReceipt(t, z)

	// sub/b's output lands on sub, a directory: the write fails.
	fake.live = liveInputs("bob")
	fake.outputs = map[string]string{"sub/b.tmpl": "sub"}
	wantOutcome(t, "inspect changed inputs", z.Inspect(item, tg), OutcomeAdopted)
	res := z.Apply(item, tg, "")
	wantOutcome(t, "failed re-render", res, OutcomeFailed)
	if got, want := readFile(t, filepath.Join(tree, "a")), renderedBody(t, "A ", liveInputs("alice"), false); got != want {
		t.Fatalf("a after the failed re-render = %q, want the previous %q", got, want)
	}
	after := readReceipt(t, z)
	wantPlaced(t, "failed re-render", tree, after, 2)
	if after.PlacedHash != rc.PlacedHash || after.InstalledAtUTC != rc.InstalledAtUTC {
		t.Fatalf("receipt rewritten after a failed re-render: %+v", after)
	}
	wantOutcome(t, "inspect after failure", z.Inspect(item, tg), OutcomeAdopted)

	fake.outputs = nil
	wantOutcome(t, "retry", z.Apply(item, tg, ""), OutcomeAdopted)
	wantPlaced(t, "retry", tree, readReceipt(t, z), 2)
}

// TestZipRerenderRestoresWithoutReceipt: when the receipt cannot be written
// after an in-place re-render, the rendered files get their previous bytes
// back, so the tree still matches the receipt's placed_hash and the next run
// retries instead of finding a conflict.
func TestZipRerenderRestoresWithoutReceipt(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs a directory the test user cannot write")
	}
	z, roots := newTestZip(t)
	fake := &fakeTemplates{live: liveInputs("alice")}
	z.renderer = fake
	item, archive := packVersion(t, "1.0", templated)
	tg := target(t, z, item, "skills")
	tree := filepath.Join(roots, "skills", "hello")
	wantOutcome(t, "install", z.Apply(item, tg, archive), OutcomeInstalled)
	rc := readReceipt(t, z)
	a, b := filepath.Join(tree, "a"), filepath.Join(tree, "sub", "b")
	wasA, wasB := readFile(t, a), readFile(t, b)

	// The receipt directory is read-only: the render lands, the receipt
	// cannot be written.
	dir := filepath.Join(z.ReceiptDir, "skills")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	fake.live = liveInputs("bob")
	wantOutcome(t, "inspect changed inputs", z.Inspect(item, tg), OutcomeAdopted)
	res := z.Apply(item, tg, "")
	wantOutcome(t, "re-render without receipt", res, OutcomeFailed)
	if !errors.Is(res.Err, fs.ErrPermission) {
		t.Fatalf("re-render failed for another reason: %v", res.Err)
	}
	if readFile(t, a) != wasA || readFile(t, b) != wasB {
		t.Fatalf("a = %q, sub/b = %q after the failed receipt; want the previous %q, %q", readFile(t, a), readFile(t, b), wasA, wasB)
	}
	after := readReceipt(t, z)
	wantPlaced(t, "re-render without receipt", tree, after, 2)
	if after.PlacedHash != rc.PlacedHash || after.InstalledAtUTC != rc.InstalledAtUTC {
		t.Fatalf("receipt changed after a failed re-render: %+v", after)
	}
	wantOutcome(t, "inspect after failure", z.Inspect(item, tg), OutcomeAdopted)

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "retry", z.Apply(item, tg, ""), OutcomeAdopted)
	rc2 := readReceipt(t, z)
	wantPlaced(t, "retry", tree, rc2, 2)
	if rc2.PlacedHash == rc.PlacedHash || readFile(t, b) != renderedBody(t, "B ", fake.live, false) {
		t.Fatalf("retry: receipt %+v, sub/b = %q", rc2, readFile(t, b))
	}
}

// TestWriteRendered: outputs land through a temporary file beside each
// target, a replaced file keeps its mode, a failure part way puts back
// every file already replaced and removes one that did not exist, and the
// undo of a successful write does the same.
func TestWriteRendered(t *testing.T) {
	tree := writeTree(t, map[string]string{"x": "old x", "dir/keep": "k"}, "x")
	x, y := filepath.Join(tree, "x"), filepath.Join(tree, "y")
	outs := []rendered{
		{Rendered: receipt.Rendered{File: "x"}, data: []byte("new x")},
		{Rendered: receipt.Rendered{File: "y"}, data: []byte("new y")},
	}
	undo, err := writeRendered(tree, outs)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, x) != "new x" || readFile(t, y) != "new y" {
		t.Fatal("outputs not written")
	}
	if info, err := os.Stat(x); osHasModes() && (err != nil || info.Mode().Perm() != 0o755) {
		t.Errorf("x mode = %v, want 0755 kept", info.Mode())
	}
	entries, _ := os.ReadDir(tree)
	if len(entries) != 3 {
		t.Errorf("tree holds %v, want only x, y and dir", entries)
	}

	// x is replaced, then dir, a directory, cannot be. x comes back as it
	// was; the new z is gone.
	outs = []rendered{
		{Rendered: receipt.Rendered{File: "x"}, data: []byte("newer x")},
		{Rendered: receipt.Rendered{File: "z"}, data: []byte("z")},
		{Rendered: receipt.Rendered{File: "dir"}, data: []byte("not a file")},
	}
	if failedUndo, err := writeRendered(tree, outs); err == nil || failedUndo != nil {
		t.Fatalf("writing over a directory: %v, undo %v", err, failedUndo != nil)
	}
	if readFile(t, x) != "new x" || readFile(t, filepath.Join(tree, "dir", "keep")) != "k" {
		t.Fatal("the failed write did not restore x, or touched dir")
	}
	assertAbsent(t, filepath.Join(tree, "z"))
	if info, err := os.Stat(x); osHasModes() && (err != nil || info.Mode().Perm() != 0o755) {
		t.Errorf("restored x mode = %v, want 0755", info.Mode())
	}

	// A hostile output path is refused before anything is written.
	outs = []rendered{{Rendered: receipt.Rendered{File: "../escape"}, data: []byte("x")}}
	if _, err := writeRendered(tree, outs); err == nil {
		t.Fatal("an output outside the tree was written")
	}
	assertAbsent(t, filepath.Join(filepath.Dir(tree), "escape"))

	// The first write's undo puts x back as it was and removes y.
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if readFile(t, x) != "old x" {
		t.Fatalf("x after undo = %q, want the original", readFile(t, x))
	}
	assertAbsent(t, y)
}

// TestInputsEqual: stored and live inputs compare as canonical JSON, so key
// order and whitespace do not matter, but numbers compare as written.
func TestInputsEqual(t *testing.T) {
	type live struct {
		A string `json:"a"`
		B []int  `json:"b"`
	}
	for _, tc := range []struct {
		stored string
		live   any
		want   bool
	}{
		{`{"b": [1, 2], "a": "x"}`, live{A: "x", B: []int{1, 2}}, true},
		{`{"n":12345678901234567890}`, map[string]uint64{"n": 12345678901234567890}, true},
		{`{"n":1.0}`, map[string]int{"n": 1}, false},
		{`{"n":1}`, map[string]string{"n": "1"}, false},
		{`{"a":"x"}`, live{A: "x"}, false},
	} {
		got, err := inputsEqual(json.RawMessage(tc.stored), tc.live)
		if err != nil || got != tc.want {
			t.Errorf("inputsEqual(%s, %+v) = %v, %v; want %v", tc.stored, tc.live, got, err, tc.want)
		}
	}
	if _, err := inputsEqual(json.RawMessage(`not json`), nil); err == nil {
		t.Error("unreadable stored inputs compared without error")
	}
}

// TestTemplatesRecorded: every .tmpl in the tree needs a record.
func TestTemplatesRecorded(t *testing.T) {
	tree := writeTree(t, templated)
	both := []receipt.Rendered{{File: "a", Template: "a.tmpl"}, {File: "sub/b", Template: "sub/b.tmpl"}}
	for _, tc := range []struct {
		name     string
		rendered []receipt.Rendered
		want     bool
	}{
		{"both recorded", both, true},
		{"one missing", both[:1], false},
		{"none recorded", nil, false},
	} {
		got, err := templatesRecorded(tree, testOS(), tc.rendered)
		if err != nil || got != tc.want {
			t.Errorf("%s: templatesRecorded = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	plain := writeTree(t, map[string]string{"SKILL.md": "x"})
	if got, err := templatesRecorded(plain, testOS(), nil); err != nil || !got {
		t.Errorf("tree without templates: %v, %v", got, err)
	}
}
