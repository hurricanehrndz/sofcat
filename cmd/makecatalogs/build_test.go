package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/admin"
	"go.yaml.in/yaml/v4"
)

// buildSource lays out a valid build source in a temp dir and returns it:
// one zip item, a hand-written package-info, a template, a manifest and an
// executable condition script.
func buildSource(t *testing.T) string {
	t.Helper()
	source := writeFiles(t, t.TempDir(), map[string]string{
		"placements.yaml":            testPlacements,
		"items/hello.yaml":           testItem,
		"item-trees/hello/SKILL.md":  "hello\n",
		"packages-info/Chrome.yaml":  chromeInfo,
		"templates/index.json.tmpl":  "{}\n",
		"manifests/site_default":     "catalogs: [skills]\nmanaged_installs: [hello]\n",
		"conditions/darwin/10-facts": macScript,
	})
	if err := os.Chmod(filepath.Join(source, "conditions", "darwin", "10-facts"), 0o755); err != nil {
		t.Fatal(err)
	}
	return source
}

func runBuildIn(source, out string) (code int, stderr string) {
	var o, e bytes.Buffer
	code = run([]string{"build", source, "-o", out}, &o, &e)
	return code, e.String()
}

// A build is a complete repository: the source's files, the zips and the
// generated package-info, compiled and passing -check as it stands.
func TestBuild(t *testing.T) {
	source := buildSource(t)
	out := filepath.Join(t.TempDir(), "repo")
	if code, stderr := runBuildIn(source, out); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}

	for _, rel := range []string{
		"placements.yaml", "packages-info/Chrome.yaml", "templates/index.json.tmpl",
		"manifests/site_default", "catalogs/skills.yaml", "catalogs/production.yaml",
	} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(rel))); err != nil {
			t.Errorf("build lacks %s: %v", rel, err)
		}
	}
	// A device runs the condition script as served: it must stay executable.
	// Windows has no executable bit to keep.
	if fi, err := os.Stat(filepath.Join(out, "conditions", "darwin", "10-facts")); err != nil || (runtime.GOOS != "windows" && fi.Mode()&0o111 == 0) {
		t.Errorf("condition script not copied executable: %v %v", fi, err)
	}

	body, err := os.ReadFile(filepath.Join(out, "packages-info", "hello.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "placements:") {
		t.Errorf("generated package-info keeps placements:\n%s", body)
	}
	var info admin.PackageInfo
	if err = yaml.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	zip := info.Item.Installer.Variants["codex-skills"]["windows"]
	if !strings.HasPrefix(zip.Location, "packages/") || zip.Hash != sha256File(t, filepath.Join(out, filepath.FromSlash(zip.Location))) {
		t.Errorf("variant %+v does not name a zip under packages/ in the build", zip)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-check", out}, &stdout, &stderr); code != 0 {
		t.Errorf("-check on the build: exit %d: %s", code, stderr.String())
	}
}

// A rebuild starts from nothing, so a zip an earlier version of an item left
// behind does not linger in the published repository.
func TestBuildReplacesPreviousBuild(t *testing.T) {
	source := buildSource(t)
	out := filepath.Join(t.TempDir(), "repo")
	if code, stderr := runBuildIn(source, out); code != 0 {
		t.Fatalf("first build: exit %d: %s", code, stderr)
	}
	stale, _ := filepath.Glob(filepath.Join(out, "packages", "*.zip"))
	if len(stale) != 1 {
		t.Fatalf("first build wrote zips %v, want 1", stale)
	}
	writeFiles(t, source, map[string]string{"item-trees/hello/SKILL.md": "hello again\n"})
	if code, stderr := runBuildIn(source, out); code != 0 {
		t.Fatalf("rebuild: exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(stale[0]); !os.IsNotExist(err) {
		t.Errorf("rebuild kept the stale %s", stale[0])
	}
}

// -o is wiped: a directory that does not look like a previous build might be
// anything, and the source must never be the thing wiped.
func TestBuildRefusesToWipe(t *testing.T) {
	source := buildSource(t)
	notABuild := writeFiles(t, t.TempDir(), map[string]string{"notes.txt": "mine"})
	// catalogs/ in the source and its parent, so only the source guard stops them.
	for _, dir := range []string{source, filepath.Dir(source)} {
		if err := os.MkdirAll(filepath.Join(dir, "catalogs"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct{ out, want string }{
		"non-empty without catalogs/": {notABuild, "does not look like a previous build"},
		"the source":                  {source, "would replace the source"},
		"an ancestor of the source":   {filepath.Dir(source), "would replace the source"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stderr := runBuildIn(source, tc.out)
			if code == 0 || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit %d, stderr %q; want non-zero and %q", code, stderr, tc.want)
			}
		})
	}
	if body, err := os.ReadFile(filepath.Join(notABuild, "notes.txt")); err != nil || string(body) != "mine" {
		t.Errorf("notes.txt touched: %q %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(source, "items", "hello.yaml")); err != nil {
		t.Errorf("source touched: %v", err)
	}

	// Inside the source, as a public/ directory, is fine.
	if code, stderr := runBuildIn(source, filepath.Join(source, "public")); code != 0 {
		t.Errorf("build into <source>/public: exit %d: %s", code, stderr)
	}
}

// A build that would not pass -check is not ready to publish, so build
// fails on what a plain makecatalogs run only warns about.
func TestBuildFailsCheck(t *testing.T) {
	source := buildSource(t)
	writeFiles(t, source, map[string]string{"packages-info/nocatalog.yaml": "item_name: nocatalog\nversion: 1.0.0\n"})
	code, stderr := runBuildIn(source, filepath.Join(t.TempDir(), "repo"))
	if code == 0 || !strings.Contains(stderr, "error:") || !strings.Contains(stderr, "no catalog; skipped") {
		t.Errorf("exit %d, stderr %q; want the check to fail", code, stderr)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("problem reported twice, as a warning too: %s", stderr)
	}
}

// A build that fails after wiping -o, here on a hand-written package-info
// the catalogs cannot read, still leaves a directory build recognises, so
// fixing the source and rerunning is enough.
func TestBuildRerunsAfterFailure(t *testing.T) {
	source := buildSource(t)
	writeFiles(t, source, map[string]string{"packages-info/broken.yaml": "item_name: [\n"})
	out := filepath.Join(t.TempDir(), "repo")
	// Twice: once creating -o, once wiping what the first failure left.
	for range 2 {
		code, stderr := runBuildIn(source, out)
		if code == 0 || strings.Contains(stderr, "refusing to wipe") {
			t.Fatalf("build with a broken package-info: exit %d: %s", code, stderr)
		}
	}
	if err := os.Remove(filepath.Join(source, "packages-info", "broken.yaml")); err != nil {
		t.Fatal(err)
	}
	if code, stderr := runBuildIn(source, out); code != 0 {
		t.Errorf("rerun after fixing the source: exit %d: %s", code, stderr)
	}
}

// build plans every item before it touches -o, so a mistake in the source
// leaves the previous build, or nothing, in place.
func TestBuildRefusals(t *testing.T) {
	tests := map[string]struct {
		change func(t *testing.T, source string)
		want   string
	}{
		"item_name other than the file name": {func(t *testing.T, source string) {
			t.Helper()
			writeFiles(t, source, map[string]string{"items/hello-copy.yaml": testItem})
		}, `hello-copy.yaml: item_name "hello" does not match the file name "hello-copy"`},
		"generated package-info over a hand-written one": {func(t *testing.T, source string) {
			t.Helper()
			writeFiles(t, source, map[string]string{"packages-info/hello.yaml": chromeInfo})
		}, "would be generated over it"},
		"tree with no item": {func(t *testing.T, source string) {
			t.Helper()
			writeFiles(t, source, map[string]string{"item-trees/helo/SKILL.md": "typo\n"})
		}, `has item_name "helo"`},
		"item with no tree": {func(t *testing.T, source string) {
			t.Helper()
			if err := os.RemoveAll(filepath.Join(source, "item-trees", "hello")); err != nil {
				t.Fatal(err)
			}
		}, filepath.Join("item-trees", "hello")},
		"item without placements": {func(t *testing.T, source string) {
			t.Helper()
			writeFiles(t, source, map[string]string{"items/hello.yaml": strings.Replace(testItem, "placements: [claude-code-skills, codex-skills]\n", "", 1)})
		}, "placements must list"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			source := buildSource(t)
			tc.change(t, source)
			out := filepath.Join(t.TempDir(), "repo")
			code, stderr := runBuildIn(source, out)
			if code == 0 {
				t.Fatal("exit 0")
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr %q does not say %q", stderr, tc.want)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("build wrote %s on failure", out)
			}
		})
	}
}
