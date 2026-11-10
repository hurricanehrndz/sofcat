package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/admin"
	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"go.yaml.in/yaml/v4"
)

const testPlacements = `
placements:
  claude-code-skills:
    paths:
      darwin: /Library/Application Support/ClaudeCode/.claude/skills
      windows: ${ProgramFiles}\ClaudeCode\.claude\skills
  codex-skills:
    paths:
      windows: ${USERPROFILE}\.agents\skills
    scope: {windows: user}
`

const testItem = "item_name: hello\ncatalog: skills\nversion: 1.0.0\ndisplay_name: Hello\n"

// writeFiles writes files (slash paths relative to root) and returns root.
func writeFiles(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// zipItemFixture lays out a valid zip-item input in a temp dir and returns
// it. Paths under it: placements.yaml, item.yaml, item-trees/.
func zipItemFixture(t *testing.T) string {
	t.Helper()
	return writeFiles(t, t.TempDir(), map[string]string{
		"placements.yaml": testPlacements,
		"item.yaml":       testItem,
		"item-trees/claude-code-skills/darwin/SKILL.md":  "claude\n",
		"item-trees/claude-code-skills/windows/SKILL.md": "claude\n",
		"item-trees/codex-skills/windows/SKILL.md":       "codex\n",
	})
}

func runZipItemIn(dir, out string) (code int, stdout, stderr string) {
	var o, e bytes.Buffer
	code = run([]string{
		"zip-item",
		"--placements", filepath.Join(dir, "placements.yaml"),
		"--item", filepath.Join(dir, "item.yaml"),
		"--trees", filepath.Join(dir, "item-trees"),
		"--location-prefix", "packages/skills",
		"-o", out,
	}, &o, &e)
	return code, o.String(), e.String()
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func TestPackAndTreeHash(t *testing.T) {
	tree := writeFiles(t, t.TempDir(), map[string]string{"SKILL.md": "hi\n", "a/b.txt": "b\n"})
	for _, goos := range []string{"darwin", "windows"} {
		out := filepath.Join(t.TempDir(), "x.zip")
		var stdout, stderr bytes.Buffer
		if code := run([]string{"pack", "--os", goos, tree, "-o", out}, &stdout, &stderr); code != 0 {
			t.Fatalf("pack exit %d: %s", code, stderr.String())
		}
		var packed struct {
			Hash     string `json:"hash"`
			TreeHash string `json:"tree_hash"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &packed); err != nil {
			t.Fatalf("pack output %q: %v", stdout.String(), err)
		}
		if packed.Hash != sha256File(t, out) {
			t.Errorf("hash %s is not the zip's sha256", packed.Hash)
		}

		stdout.Reset()
		if code := run([]string{"tree-hash", "--os", goos, tree}, &stdout, &stderr); code != 0 {
			t.Fatalf("tree-hash exit %d: %s", code, stderr.String())
		}
		if got := strings.TrimSpace(stdout.String()); got != packed.TreeHash {
			t.Errorf("tree-hash %s, pack tree_hash %s", got, packed.TreeHash)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"pack", "--os", "linux", tree, "-o", filepath.Join(t.TempDir(), "x.zip")}, &stdout, &stderr); code == 0 {
		t.Error("pack accepted --os linux")
	}
	if code := run([]string{"tree-hash", t.TempDir()}, &stdout, &stderr); code == 0 {
		t.Error("tree-hash accepted an empty tree")
	}
}

// zip-item checks everything before it writes: any refusal leaves <out>
// untouched, so a failed CI run never publishes half an item.
func TestZipItemRefusals(t *testing.T) {
	tests := map[string]struct {
		change func(t *testing.T, dir string)
		want   string
	}{
		"item already has an installer": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item.yaml": testItem + "installer: {type: msi, location: x.msi}\n"})
		}, "already has an installer"},
		"placement not in placements.yaml": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item-trees/cursor-skills/darwin/SKILL.md": "x"})
		}, `placement "cursor-skills" is not in`},
		"OS directory not darwin or windows": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item-trees/claude-code-skills/linux/SKILL.md": "x"})
		}, "must be darwin or windows"},
		"placement has no path for the OS": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item-trees/codex-skills/darwin/SKILL.md": "x"})
		}, "has no darwin path"},
		"empty tree": {func(t *testing.T, dir string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(dir, "item-trees", "codex-skills", "windows", "empty"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "item-trees", "codex-skills", "windows", "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		}, "no files"},
		"tree wrapped in a directory named after the item": {func(t *testing.T, dir string) {
			t.Helper()
			if err := os.RemoveAll(filepath.Join(dir, "item-trees", "codex-skills", "windows")); err != nil {
				t.Fatal(err)
			}
			writeFiles(t, dir, map[string]string{"item-trees/codex-skills/windows/hello/SKILL.md": "x"})
		}, "wrapped in a hello/ directory"},
		"x beside x.tmpl": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item-trees/codex-skills/windows/x": "shipped", "item-trees/codex-skills/windows/x.tmpl": "rendered"})
		}, "x.tmpl: the tree also ships x"},
		"directory x beside x.tmpl": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item-trees/codex-skills/windows/x/y": "shipped", "item-trees/codex-skills/windows/x.tmpl": "rendered"})
		}, "x.tmpl: the tree also ships a directory x"},
		"bare .tmpl": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item-trees/codex-skills/windows/.tmpl": "x"})
		}, ".tmpl: a template needs a name"},
		"template reads a missing fact": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"item-trees/claude-code-skills/darwin/NOTE.md.tmpl": `{{ fact "missing" }}`})
		}, `fact "missing": no such fact`},
		"template file missing beside placements.yaml": {func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{"placements.yaml": testPlacements +
				"    files:\n      - path: index.json\n        template_file: templates/index.json.tmpl\n"})
		}, `placement "codex-skills": files[0]: template_file "templates/index.json.tmpl"`},
		"no placement directories": {func(t *testing.T, dir string) {
			t.Helper()
			if err := os.RemoveAll(filepath.Join(dir, "item-trees")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "item-trees"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "no placement directories"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := zipItemFixture(t)
			tc.change(t, dir)
			out := filepath.Join(t.TempDir(), "repo")
			code, stdout, stderr := runZipItemIn(dir, out)
			if code == 0 {
				t.Fatalf("exit 0, stdout:\n%s", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr %q does not say %q", stderr, tc.want)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("zip-item wrote %s on failure", out)
			}
		})
	}
}

// Trees that hash the same pack to the same bytes, so they share one zip.
func TestZipItemSharesIdenticalTrees(t *testing.T) {
	dir := zipItemFixture(t)
	writeFiles(t, dir, map[string]string{"item-trees/codex-skills/windows/SKILL.md": "claude\n"})
	out := t.TempDir()
	code, stdout, stderr := runZipItemIn(dir, out)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var info admin.PackageInfo
	if err := yaml.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatal(err)
	}
	variants := info.Item.Installer.Variants
	claude, codex := variants["claude-code-skills"]["windows"], variants["codex-skills"]["windows"]
	if claude.Location == "" || claude != codex {
		t.Errorf("claude %+v and codex %+v differ", claude, codex)
	}
	// Without executable bits the darwin tree hashes the same too.
	if variants["claude-code-skills"]["darwin"] != claude {
		t.Errorf("darwin %+v, windows %+v", variants["claude-code-skills"]["darwin"], claude)
	}
	zips, _ := filepath.Glob(filepath.Join(out, "packages", "skills", "*.zip"))
	if len(zips) != 1 {
		t.Fatalf("wrote %d zips, want 1: %v", len(zips), zips)
	}
	if want := "packages/skills/" + filepath.Base(zips[0]); claude.Location != want {
		t.Errorf("location %s, want %s", claude.Location, want)
	}
	if claude.Hash != sha256File(t, zips[0]) {
		t.Errorf("hash %s is not the zip's sha256", claude.Hash)
	}
}

// Keys catalog.Item does not know, such as ones a later sofcat reads, must
// reach the published pkginfo rather than vanish in a struct round trip.
func TestZipItemKeepsAuthorKeys(t *testing.T) {
	dir := zipItemFixture(t)
	writeFiles(t, dir, map[string]string{
		"item.yaml": testItem + "custom_action_labels: {install: Add}\nx_author_note: keep me\n",
	})
	code, stdout, stderr := runZipItemIn(dir, t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var got struct {
		Labels map[string]string `yaml:"custom_action_labels"`
		Note   string            `yaml:"x_author_note"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.Labels["install"] != "Add" || got.Note != "keep me" {
		t.Errorf("author keys lost: %+v in\n%s", got, stdout)
	}
}

// zip-item output is a package-info file the repo's -check gate accepts.
func TestZipItemRoundTripPassesCheck(t *testing.T) {
	dir := zipItemFixture(t)
	repo := writeFiles(t, t.TempDir(), map[string]string{"placements.yaml": testPlacements})
	code, stdout, stderr := runZipItemIn(dir, repo)
	if code != 0 {
		t.Fatalf("zip-item exit %d: %s", code, stderr)
	}
	writeFiles(t, repo, map[string]string{"packages-info/hello.yaml": stdout})

	var out, errOut bytes.Buffer
	if code := run([]string{"-check", repo}, &out, &errOut); code != 0 {
		t.Fatalf("-check exit %d: %s", code, errOut.String())
	}
}

// The examples are generated by zip-item from the checked-in item tree;
// regenerating them must reproduce them byte for byte.
func TestExampleZipItemsMatchGenerator(t *testing.T) {
	examples := filepath.Join("..", "..", "examples")
	itemDir := filepath.Join(examples, "zip-items")
	generated := filepath.Join(itemDir, "generated")
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"zip-item",
		"--placements", filepath.Join(examples, "placements.yaml"),
		"--item", filepath.Join(itemDir, "acme-wallpapers.yaml"),
		"--trees", filepath.Join(itemDir, "item-trees", "acme-wallpapers"),
		"--location-prefix", "packages/wallpapers",
		"-o", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	want, err := os.ReadFile(filepath.Join(generated, "acme-wallpapers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != string(want) {
		t.Errorf("examples/zip-items/generated/acme-wallpapers.yaml is stale; regenerate it (see site/src/content/docs/zip-items/building.md). Got:\n%s", stdout.String())
	}
	// example_catalog.yaml shows the same item as a compiled catalog entry.
	var info admin.PackageInfo
	if err = yaml.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(examples, "example_catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var exampleCatalog map[string]catalog.Item
	if err := yaml.Unmarshal(body, &exampleCatalog); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exampleCatalog["acme-wallpapers"].Installer, info.Item.Installer) {
		t.Errorf("examples/example_catalog.yaml acme-wallpapers installer is stale")
	}

	got, _ := filepath.Glob(filepath.Join(out, "packages", "wallpapers", "*.zip"))
	checkedIn, _ := filepath.Glob(filepath.Join(generated, "packages", "wallpapers", "*.zip"))
	if len(got) != len(checkedIn) {
		t.Fatalf("generated %d zips, %d checked in", len(got), len(checkedIn))
	}
	for i := range got {
		if filepath.Base(got[i]) != filepath.Base(checkedIn[i]) || sha256File(t, got[i]) != sha256File(t, checkedIn[i]) {
			t.Errorf("zip %s differs from checked-in %s", got[i], checkedIn[i])
		}
	}
}

func TestCheckZipItems(t *testing.T) {
	const variant = "{location: packages/a.zip, hash: abc, tree_hash: 'sha256:def'}"
	item := func(installer string) string {
		return "item_name: hello\ncatalog: skills\nversion: 1.0.0\ninstaller:\n" + installer
	}
	tests := map[string]struct {
		placements string // "" means no placements.yaml
		info       string
		want       string // "" means -check passes
	}{
		"valid":       {testPlacements, item("  type: zip\n  variants: {claude-code-skills: {darwin: " + variant + "}}\n"), ""},
		"no variants": {testPlacements, item("  type: zip\n"), "has no variants"},
		"variant missing tree_hash": {
			testPlacements, item("  type: zip\n  variants: {claude-code-skills: {darwin: {location: a.zip, hash: abc}}}\n"),
			"needs location, hash and tree_hash",
		},
		"unknown OS": {
			testPlacements, item("  type: zip\n  variants: {claude-code-skills: {linux: " + variant + "}}\n"),
			"OS must be darwin or windows",
		},
		"placement not in placements.yaml": {
			testPlacements, item("  type: zip\n  variants: {cursor-skills: {darwin: " + variant + "}}\n"),
			`placement "cursor-skills" is not in placements.yaml`,
		},
		"zip item without placements.yaml": {
			"", item("  type: zip\n  variants: {claude-code-skills: {darwin: " + variant + "}}\n"),
			"no placements.yaml",
		},
		"invalid placements.yaml without zip items": {
			"placements:\n  p: {kind: directory, paths: {darwin: /p}}\n", chromeInfo, "field kind not found",
		},
	}
	t.Run("normal run warns", func(t *testing.T) {
		repo := writeRepo(t, map[string]string{
			"hello.yaml": item("  type: zip\n  variants: {cursor-skills: {darwin: " + variant + "}}\n"),
		})
		writeFiles(t, repo, map[string]string{"placements.yaml": testPlacements})
		var stdout, stderr bytes.Buffer
		if code := run([]string{repo}, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d, stderr: %s", code, stderr.String())
		}
		want := `placement "cursor-skills" is not in placements.yaml`
		if !strings.Contains(stderr.String(), "warning:") || !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q, want a warning: %q", stderr.String(), want)
		}
	})
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			repo := writeRepo(t, map[string]string{"hello.yaml": tc.info})
			if tc.placements != "" {
				writeFiles(t, repo, map[string]string{"placements.yaml": tc.placements})
			}
			var stdout, stderr bytes.Buffer
			code := run([]string{"-check", repo}, &stdout, &stderr)
			if tc.want == "" {
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr.String())
				}
				return
			}
			if code != 1 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit %d, stderr %q; want 1 and %q", code, stderr.String(), tc.want)
			}
		})
	}
}

// zip-item renders every template in a tree as the device would, with the
// sample facts, check_facts, the sample user in user scope and the managed
// flag both ways; a failure names the item, the OS and the template.
func TestZipItemChecksTemplates(t *testing.T) {
	dir := zipItemFixture(t)
	writeFiles(t, dir, map[string]string{
		"placements.yaml": "check_facts: {assigned_user: jdoe}\n" + testPlacements,
		"item-trees/codex-skills/windows/NOTE.md.tmpl": `{{ .Item.Name }} {{ .Item.Version }} for {{ .User }} ({{ fact "assigned_user" }}, {{ fact "os" }}) managed={{ .Item.Managed }}` +
			`{{ if cond "os == \"windows\" && os_build > 1" }} win{{ end }}`,
		"item-trees/claude-code-skills/darwin/.claude-plugin/plugin.json.tmpl": `{"installationPreference": {{ if .Item.Managed }}"auto_install"{{ else }}"available"{{ end }}, "user": {{ json .User }}}`,
	})
	if code, _, stderr := runZipItemIn(dir, t.TempDir()); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}

	// A fact only one managed branch reads still fails: both are rendered.
	writeFiles(t, dir, map[string]string{
		"item-trees/claude-code-skills/darwin/.claude-plugin/plugin.json.tmpl": `{{ if .Item.Managed }}{}{{ else }}{{ fact "missing" }}{{ end }}`,
	})
	out := filepath.Join(t.TempDir(), "repo")
	code, _, stderr := runZipItemIn(dir, out)
	if code == 0 {
		t.Fatal("a template reading a missing fact packed")
	}
	for _, want := range []string{`item "hello"`, "on darwin", "plugin.json.tmpl", "managed false", `fact "missing": no such fact`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("zip-item wrote on failure")
	}
}

// -check opens every zip the repository holds and renders its templates the
// way zip-item does, so a zip packed elsewhere with a broken template fails
// the check naming the item, the placement, the OS and the template. A zip
// the repository does not hold is not checked.
func TestCheckZipTemplates(t *testing.T) {
	pack := func(t *testing.T, repo string, files map[string]string, goos string) string {
		t.Helper()
		tree := writeFiles(t, t.TempDir(), files)
		zipPath := filepath.Join(repo, "packages", "skills", "hello.zip")
		if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
			t.Fatal(err)
		}
		packed, err := artifact.PackFile(tree, goos, zipPath)
		if err != nil {
			t.Fatal(err)
		}
		return "item_name: hello\ncatalog: skills\nversion: 1.0.0\ninstaller:\n  type: zip\n  variants:\n    claude-code-skills:\n      " +
			goos + ": {location: packages/skills/hello.zip, hash: " + packed.Hash + ", tree_hash: '" + packed.TreeHash + "'}\n"
	}
	run := func(t *testing.T, repo string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run([]string{"-check", repo}, &stdout, &stderr)
		return code, stderr.String()
	}

	repo := t.TempDir()
	writeFiles(t, repo, map[string]string{"placements.yaml": testPlacements})
	good := pack(t, repo, map[string]string{"SKILL.md": "s", "NOTE.md.tmpl": `{{ .Item.Name }} on {{ fact "os" }}`}, "darwin")
	writeFiles(t, repo, map[string]string{"packages-info/hello.yaml": good})
	if code, stderr := run(t, repo); code != 0 {
		t.Fatalf("good template: exit %d: %s", code, stderr)
	}

	bad := pack(t, repo, map[string]string{"SKILL.md": "s", "sub/NOTE.md.tmpl": `{{ fact "missing" }}`}, "windows")
	writeFiles(t, repo, map[string]string{"packages-info/hello.yaml": bad})
	code, stderr := run(t, repo)
	if code != 1 {
		t.Fatalf("bad template: exit %d: %s", code, stderr)
	}
	for _, want := range []string{`zip item "hello"`, `placement "claude-code-skills"`, "on windows", "sub/NOTE.md.tmpl", `fact "missing": no such fact`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}

	both := pack(t, repo, map[string]string{"x": "shipped", "x.tmpl": "rendered"}, "windows")
	writeFiles(t, repo, map[string]string{"packages-info/hello.yaml": both})
	if code, stderr := run(t, repo); code != 1 || !strings.Contains(stderr, "x.tmpl: the tree also ships x") {
		t.Errorf("x beside x.tmpl: exit %d: %s", code, stderr)
	}

	// The zip is gone: nothing to open, nothing to report.
	if err := os.Remove(filepath.Join(repo, "packages", "skills", "hello.zip")); err != nil {
		t.Fatal(err)
	}
	if code, stderr := run(t, repo); code != 0 {
		t.Errorf("missing zip: exit %d: %s", code, stderr)
	}
}
