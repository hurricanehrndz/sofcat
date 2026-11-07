package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
)

// desktopTree is a Claude Desktop style plugin: installationPreference from
// the managed flag, an MCP header from an admin fact and a field chosen by
// a cond over a built-in, and a note that reads the rest of the context.
var desktopTree = map[string]string{
	"SKILL.md":                        "# hello\n",
	".claude-plugin/plugin.json.tmpl": `{"name": {{ json .Item.Name }}, "version": {{ json .Item.Version }}, "installationPreference": {{ if .Item.Managed }}"auto_install"{{ else }}"available"{{ end }}}`,
	".mcp.json.tmpl":                  `{"mcpServers": {"proxy": {"headers": {"x-litellm-api-key": "Bearer {{ fact "assigned_user" }}/claudecode", "x-platform": {{ if cond "os == \"windows\"" }}"windows"{{ else }}"macos"{{ end }}}}}}`,
	"NOTE.md.tmpl":                    "user={{ .User }} scope={{ .Scope }} placement={{ .Placement.Name }} path={{ .Item.Path }} hash={{ .Item.TreeHash }}\n",
}

func testFacts() conditions.Facts {
	return conditions.Facts{"assigned_user": "jdoe", "os": testOS(), "ipv4_address": []string{"10.0.0.1"}}
}

// recordInputs decodes a rendered record's inputs.
func recordInputs(t *testing.T, rec receipt.Rendered) treeInputs {
	t.Helper()
	var in treeInputs
	if err := json.Unmarshal(rec.Inputs, &in); err != nil {
		t.Fatalf("inputs of %s: %v: %s", rec.File, err, rec.Inputs)
	}
	return in
}

// sameFile reports whether path is still the file info describes: a
// re-render writes a new file and renames it over the old one.
func sameFile(t *testing.T, path string, info os.FileInfo) bool {
	t.Helper()
	now, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(info, now)
}

func stat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// TestZipTreeWithoutTemplates: with the real renderer, a tree that ships no
// template is placed as shipped and a run with nothing changed leaves its
// receipt alone.
func TestZipTreeWithoutTemplates(t *testing.T) {
	z, roots := newTestZip(t)
	z.Facts = testFacts()
	item, archive := packVersion(t, "1.0", map[string]string{"SKILL.md": "plain"})
	tg := target(t, z, item, "skills")
	wantOutcome(t, "install", z.Apply(item, tg, archive), OutcomeInstalled)
	rc := readReceipt(t, z)
	wantPlaced(t, "install", filepath.Join(roots, "skills", "hello"), rc, 0)
	if rc.PlacedHash != rc.TreeHash {
		t.Fatalf("receipt = %+v", rc)
	}
	wantOutcome(t, "inspect", z.Inspect(item, tg), OutcomeCurrent)
	wantOutcome(t, "apply again", z.Apply(item, tg, ""), OutcomeCurrent)
	if again := readReceipt(t, z); !reflect.DeepEqual(again, rc) {
		t.Fatalf("receipt rewritten: %+v, was %+v", again, rc)
	}
}

// TestZipTreeTemplates: the templates a tree ships are rendered beside
// themselves in staging with the item, the OS and the facts; each record
// stores only the facts it read, so a change to another fact is nothing, a
// change to a read fact or to the managed flag re-renders that file alone,
// in place and without the archive, and a run without facts refuses the tree
// naming the fact.
func TestZipTreeTemplates(t *testing.T) {
	z, roots := newTestZip(t)
	z.Facts = testFacts()
	z.Managed = func(string) bool { return true }
	item, archive := packVersion(t, "1.0", desktopTree)
	tg := target(t, z, item, "skills")
	tree := filepath.Join(roots, "skills", "hello")
	file := func(name string) string { return filepath.Join(tree, filepath.FromSlash(name)) }

	wantOutcome(t, "install", z.Apply(item, tg, archive), OutcomeInstalled)
	rc := readReceipt(t, z)
	wantPlaced(t, "install", tree, rc, 3)
	if rc.Rendered[0].File != ".claude-plugin/plugin.json" || rc.Rendered[0].Template != ".claude-plugin/plugin.json.tmpl" ||
		rc.Rendered[1].File != ".mcp.json" || rc.Rendered[2].File != "NOTE.md" {
		t.Fatalf("records = %+v", rc.Rendered)
	}
	if !exists(file(".mcp.json.tmpl")) {
		t.Fatal("the template left the tree")
	}
	wantPlugin := "{\n  \"installationPreference\": \"auto_install\",\n  \"name\": \"hello\",\n  \"version\": \"1.0\"\n}\n"
	if got := readFile(t, file(".claude-plugin/plugin.json")); got != wantPlugin {
		t.Fatalf("plugin.json = %q, want canonical %q", got, wantPlugin)
	}
	platform := "macos"
	if testOS() == placement.Windows {
		platform = "windows"
	}
	if got := readFile(t, file(".mcp.json")); !strings.Contains(got, `"x-litellm-api-key": "Bearer jdoe/claudecode"`) || !strings.Contains(got, `"x-platform": "`+platform+`"`) {
		t.Fatalf(".mcp.json = %q", got)
	}
	if got, want := readFile(t, file("NOTE.md")), "user= scope=machine placement=skills path="+tree+" hash="+tg.Variant.TreeHash+"\n"; got != want {
		t.Fatalf("NOTE.md = %q, want %q", got, want)
	}
	// Each record stores the context and only the facts it read.
	plugin, mcp := recordInputs(t, rc.Rendered[0]), recordInputs(t, rc.Rendered[1])
	wantItem := treeInputsItem{Name: "hello", Version: "1.0", Managed: true}
	if plugin.OS != testOS() || plugin.Scope != "machine" || plugin.User != "" || plugin.Item != wantItem || len(plugin.Facts) != 0 {
		t.Fatalf("plugin.json inputs = %+v", plugin)
	}
	wantRead := conditions.Facts{"assigned_user": "jdoe", "os": testOS()}
	if !reflect.DeepEqual(mcp.Facts, wantRead) {
		t.Fatalf(".mcp.json read %v, want %v", mcp.Facts, wantRead)
	}
	wantOutcome(t, "inspect", z.Inspect(item, tg), OutcomeCurrent)

	// A fact nothing read changes: nothing to do.
	z.Facts["ipv4_address"] = []string{"10.9.9.9"}
	wantOutcome(t, "unread fact", z.Apply(item, tg, ""), OutcomeCurrent)

	// A read fact changes: .mcp.json alone is re-rendered, in place.
	before := map[string]os.FileInfo{}
	for _, name := range []string{"SKILL.md", ".claude-plugin/plugin.json", ".mcp.json", "NOTE.md"} {
		before[name] = stat(t, file(name))
	}
	z.Facts["assigned_user"] = "asmith"
	wantOutcome(t, "inspect changed fact", z.Inspect(item, tg), OutcomeAdopted)
	wantOutcome(t, "re-render fact", z.Apply(item, tg, ""), OutcomeAdopted)
	rc2 := readReceipt(t, z)
	wantPlaced(t, "re-render fact", tree, rc2, 3)
	if rc2.PlacedHash == rc.PlacedHash || rc2.TreeHash != rc.TreeHash || !strings.Contains(readFile(t, file(".mcp.json")), "Bearer asmith/claudecode") {
		t.Fatalf("after the fact change: receipt %+v, .mcp.json %q", rc2, readFile(t, file(".mcp.json")))
	}
	for _, name := range []string{"SKILL.md", ".claude-plugin/plugin.json", "NOTE.md"} {
		if !sameFile(t, file(name), before[name]) {
			t.Errorf("the fact change rewrote %s", name)
		}
	}
	wantOutcome(t, "inspect after fact", z.Inspect(item, tg), OutcomeCurrent)

	// The managed flag flips: plugin.json alone is re-rendered.
	before[".mcp.json"] = stat(t, file(".mcp.json"))
	z.Managed = func(string) bool { return false }
	wantOutcome(t, "inspect flipped", z.Inspect(item, tg), OutcomeAdopted)
	wantOutcome(t, "re-render flip", z.Apply(item, tg, ""), OutcomeAdopted)
	rc3 := readReceipt(t, z)
	if rc3.Managed || !strings.Contains(readFile(t, file(".claude-plugin/plugin.json")), `"available"`) || recordInputs(t, rc3.Rendered[0]).Item.Managed {
		t.Fatalf("after the flip: receipt %+v, plugin.json %q", rc3, readFile(t, file(".claude-plugin/plugin.json")))
	}
	for _, name := range []string{"SKILL.md", ".mcp.json", "NOTE.md"} {
		if !sameFile(t, file(name), before[name]) {
			t.Errorf("the flip rewrote %s", name)
		}
	}
	wantOutcome(t, "inspect after flip", z.Inspect(item, tg), OutcomeCurrent)

	// Without facts (a run whose scripts went missing) the stored facts no
	// longer match, and the re-render is refused naming the fact; the tree
	// still matches its receipt.
	z.Facts = nil
	res := z.Apply(item, tg, "")
	wantOutcome(t, "no facts", res, OutcomeRefused)
	if res.Err == nil || !strings.Contains(res.Err.Error(), `fact "assigned_user"`) {
		t.Fatalf("refusal = %v, want the fact named", res.Err)
	}
	wantPlaced(t, "no facts", tree, readReceipt(t, z), 3)
	z.Facts = conditions.Facts{"assigned_user": "asmith", "os": testOS()}
	wantOutcome(t, "facts back", z.Inspect(item, tg), OutcomeCurrent)
}

// TestZipTreeUser: .User is the installer's user in user scope and empty in
// machine scope, and the record stores it either way.
func TestZipTreeUser(t *testing.T) {
	z, _ := newTestZip(t)
	z.Facts = testFacts()
	z.User = "alice"
	item, _ := packVersion(t, "1.0", map[string]string{"SKILL.md": "s"})
	dir := writeTree(t, map[string]string{"NOTE.md.tmpl": "user={{ .User }}"})
	root := t.TempDir()
	for scope, want := range map[string]string{placement.ScopeUser: "alice", placement.ScopeMachine: ""} {
		tg := Target{Placement: "codex", Spec: placement.Spec{Scope: map[string]string{testOS(): scope}}, Known: true}
		outs, err := treeTemplates{z}.render(item, tg, root, dir, true)
		if err != nil || len(outs) != 1 {
			t.Fatalf("%s: render = %+v, %v", scope, outs, err)
		}
		if got := string(outs[0].data); got != "user="+want {
			t.Errorf("%s: rendered %q, want user=%q", scope, got, want)
		}
		if in := recordInputs(t, outs[0].Rendered); in.User != want || in.Scope != scope {
			t.Errorf("%s: inputs = %+v", scope, in)
		}
	}
}

// TestZipTreeTemplateRefusals: a tree that ships x beside x.tmpl, a bare
// .tmpl or an x.tmpl.tmpl, and a template that cannot render, are refused
// with nothing placed and no receipt.
func TestZipTreeTemplateRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"x beside x.tmpl": {map[string]string{"x": "shipped", "x.tmpl": "rendered"}, "the tree also ships x"},
		"bare .tmpl":      {map[string]string{"SKILL.md": "s", "sub/.tmpl": "x"}, "a template needs a name"},
		"x.tmpl.tmpl":     {map[string]string{"x.tmpl.tmpl": "x"}, "cannot render another template"},
		"missing fact":    {map[string]string{"a.tmpl": `{{ fact "missing" }}`}, `fact "missing": no such fact`},
		"bad JSON":        {map[string]string{"a.json.tmpl": `{"a": 1,}`}, "not JSON"},
		"does not parse":  {map[string]string{"a.tmpl": `{{ if }}`}, "a.tmpl:1"},
	} {
		t.Run(name, func(t *testing.T) {
			z, roots := newTestZip(t)
			z.Facts = testFacts()
			item, archive := packVersion(t, "1.0", tc.files)
			tg := target(t, z, item, "skills")
			res := z.Apply(item, tg, archive)
			wantOutcome(t, "install", res, OutcomeRefused)
			if res.Err == nil || !strings.Contains(res.Err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", res.Err, tc.want)
			}
			assertAbsent(t, filepath.Join(roots, "skills", "hello"))
			if _, ok, _ := receipt.Read(z.ReceiptDir, "skills", "hello"); ok {
				t.Error("a receipt was written")
			}
			if entries, _ := os.ReadDir(filepath.Join(roots, "skills")); len(entries) != 0 {
				t.Errorf("staging left behind: %v", entries)
			}
		})
	}
}

// TestZipTreeHeldKeepsManaged: a held item's render takes the managed flag
// from its receipt, not the run's Managed func, so a re-render in place for
// a changed fact records the receipt's flag and leaves the output it chose;
// released, the run's flag applies again.
func TestZipTreeHeldKeepsManaged(t *testing.T) {
	z, roots := newTestZip(t)
	z.Facts = testFacts()
	z.Managed = func(string) bool { return true }
	item, archive := packVersion(t, "1.0", desktopTree)
	tg := target(t, z, item, "skills")
	tree := filepath.Join(roots, "skills", "hello")
	plugin := func() string { return readFile(t, filepath.Join(tree, ".claude-plugin", "plugin.json")) }
	wantOutcome(t, "install", z.Apply(item, tg, archive), OutcomeInstalled)

	// The run now reports the item unmanaged, but it is held: nothing to do.
	z.Managed = func(string) bool { return false }
	z.Held = func(name string) bool { return name == "hello" }
	wantOutcome(t, "held flip", z.Apply(item, tg, ""), OutcomeCurrent)

	// A read fact changes: .mcp.json is re-rendered with the receipt's flag.
	z.Facts["assigned_user"] = "asmith"
	wantOutcome(t, "held re-render", z.Apply(item, tg, ""), OutcomeAdopted)
	rc := readReceipt(t, z)
	wantPlaced(t, "held re-render", tree, rc, 3)
	if !rc.Managed || !strings.Contains(plugin(), `"auto_install"`) || !recordInputs(t, rc.Rendered[1]).Item.Managed ||
		!strings.Contains(readFile(t, filepath.Join(tree, ".mcp.json")), "Bearer asmith/claudecode") {
		t.Fatalf("held re-render: receipt %+v, plugin.json %q", rc, plugin())
	}

	// Released, the run's flag applies: plugin.json flips.
	z.Held = nil
	wantOutcome(t, "released", z.Apply(item, tg, ""), OutcomeAdopted)
	if rc := readReceipt(t, z); rc.Managed || !strings.Contains(plugin(), `"available"`) || recordInputs(t, rc.Rendered[0]).Item.Managed {
		t.Fatalf("released: receipt %+v, plugin.json %q", rc, plugin())
	}
}

// TestZipHandPlacedShippedTreeRefused: a catalog-equal tree sofcat did not
// place is held to the shipped rules when adopted, so one holding x beside
// x.tmpl is refused with nothing written and no receipt, as at install.
func TestZipHandPlacedShippedTreeRefused(t *testing.T) {
	z, roots := newTestZip(t)
	z.Facts = testFacts()
	files := map[string]string{"x": "shipped", "x.tmpl": "rendered"}
	item, _ := packVersion(t, "1.0", files)
	tg := target(t, z, item, "skills")
	tree := filepath.Join(roots, "skills", "hello")
	if err := os.CopyFS(tree, os.DirFS(writeTree(t, files))); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, "inspect hand-placed", z.Inspect(item, tg), OutcomeAdopted)
	res := z.Apply(item, tg, "")
	wantOutcome(t, "adopt hand-placed", res, OutcomeRefused)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "the tree also ships x") {
		t.Errorf("err = %v, want the shipped file named", res.Err)
	}
	if got := readFile(t, filepath.Join(tree, "x")); got != "shipped" {
		t.Errorf("x = %q after the refusal", got)
	}
	if _, ok, _ := receipt.Read(z.ReceiptDir, "skills", "hello"); ok {
		t.Error("a receipt was written")
	}
}
