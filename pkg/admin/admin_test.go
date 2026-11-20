package admin

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"go.yaml.in/yaml/v4"
)

func TestCollectAndWriteCatalogs(t *testing.T) {
	repoPath := t.TempDir()
	packagesInfoPath := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(packagesInfoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	itemA := `
item_name: Chrome
display_name: Google Chrome
catalog: base
installer:
  type: nupkg
  location: packages/chrome/chrome.nupkg
  package_id: chrome
  hash: abc
`
	itemB := `
display_name: Agent Tool
catalog: base
installer:
  type: nupkg
  location: packages/agent/agent.nupkg
  package_id: agent
  hash: def
`
	itemSkip := `
display_name: No Catalog
installer:
  type: nupkg
  location: packages/skip/skip.nupkg
  package_id: skip
  hash: ghi
`
	if err := os.WriteFile(filepath.Join(packagesInfoPath, "Chrome.yaml"), []byte(itemA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagesInfoPath, "agent.yaml"), []byte(itemB), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagesInfoPath, "skip.yaml"), []byte(itemSkip), 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := CollectCatalogs(repoPath)
	if err != nil {
		t.Fatalf("CollectCatalogs failed: %v", err)
	}
	if len(set.Problems) != 1 {
		t.Fatalf("expected one problem for the item without a catalog, got %q", set.Problems)
	}
	if err = WriteCatalogs(repoPath, set); err != nil {
		t.Fatalf("WriteCatalogs failed: %v", err)
	}

	catalogYAML, err := os.ReadFile(filepath.Join(repoPath, "catalogs", "base.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]catalog.Item
	if err = yaml.Unmarshal(catalogYAML, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["Chrome"]; !ok {
		t.Fatalf("expected item key Chrome in generated catalog")
	}
	if _, ok := got["AgentTool"]; !ok {
		t.Fatalf("expected fallback item key AgentTool in generated catalog")
	}
	if _, ok := got["NoCatalog"]; ok {
		t.Fatalf("did not expect item without catalog to be generated")
	}
}

// The file name is how an admin finds an item's package-info, so an item_name
// that disagrees with it is reported; the item stays under its item_name.
func TestCollectCatalogsItemNameMismatch(t *testing.T) {
	repoPath := t.TempDir()
	dir := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "chrome-120.yaml")
	if err := os.WriteFile(path, []byte("item_name: chrome\ncatalog: base\ninstaller: {type: msi, location: x.msi}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := CollectCatalogs(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	want := path + `: item_name "chrome" does not match the file name "chrome-120"`
	if len(set.Problems) != 1 || set.Problems[0] != want {
		t.Errorf("problems = %v, want [%s]", set.Problems, want)
	}
	if _, ok := set.Catalogs["base"]["chrome"]; !ok {
		t.Error("item dropped from the catalog")
	}
}

func TestCollectCatalogsMissingPackagesInfo(t *testing.T) {
	repoPath := t.TempDir()
	if _, err := CollectCatalogs(repoPath); err == nil {
		t.Fatalf("expected error when packages-info is missing")
	}
}

// A malformed installable_condition is caught when the catalog is built, not
// discovered when agents silently refuse the item. The item stays in the
// catalog: agents treat the condition as false, which is safe.
func TestCollectCatalogsInvalidCondition(t *testing.T) {
	repoPath := t.TempDir()
	dir := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, cond := range map[string]string{"good": `arch == "arm64"`, "bad": `arch = "arm64"`} {
		info := "item_name: " + name + "\ncatalog: base\ninstallable_condition: '" + cond + "'\ninstaller:\n  type: msi\n  location: x.msi\n"
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(info), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := CollectCatalogs(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0], "bad.yaml: invalid installable_condition") {
		t.Errorf("problems = %v, want one for bad.yaml", set.Problems)
	}
	if got := set.Catalogs["base"]["good"].InstallableCondition; got != `arch == "arm64"` {
		t.Errorf("good condition = %q, want it carried into the catalog", got)
	}
	if _, ok := set.Catalogs["base"]["bad"]; !ok {
		t.Error("bad item dropped from the catalog")
	}
}

// A depends_check entry the agent cannot use is caught by -check: without
// display_name the deferral message has no name, without sofcat, munki or
// script nothing checks it, and a script under an unknown OS, an empty one or
// a darwin one without #! never passes.
func TestCollectCatalogsDependsCheck(t *testing.T) {
	repoPath := t.TempDir()
	dir := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	info := `item_name: tool
catalog: base
installer: {type: msi, location: x.msi}
depends_check:
  - display_name: Demo Tool
    sofcat: demo-tool
    munki: DemoTool
    script:
      windows: exit 0
      darwin: "#!/bin/sh\nexit 0\n"
  - {display_name: Mac Tool, munki: MacTool}
  - {sofcat: nameless}
  - {display_name: Nowhere}
  - display_name: Bad Scripts
    script: {linux: exit 0, windows: " ", darwin: exit 0}
`
	if err := os.WriteFile(filepath.Join(dir, "tool.yaml"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := CollectCatalogs(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"depends_check[2] has no display_name",
		"depends_check[3] needs sofcat, munki or script",
		"depends_check[4] script for darwin does not start with #!",
		`depends_check[4] script key "linux" is not one of darwin, windows`,
		"depends_check[4] script for windows is empty",
	}
	if len(set.Problems) != len(want) {
		t.Fatalf("problems = %v, want %v", set.Problems, want)
	}
	for i, w := range want {
		if !strings.Contains(set.Problems[i], w) {
			t.Errorf("problem %d = %q, want it to mention %q", i, set.Problems[i], w)
		}
	}
	got := set.Catalogs["base"]["tool"].DependsCheck
	if len(got) != 5 || got[0].Sofcat != "demo-tool" || got[0].Script["windows"] != "exit 0" {
		t.Errorf("depends_check = %+v, want it carried into the catalog", got)
	}
}

// A custom_action_labels key the storefront does not know would be ignored
// without a word, so -check reports it; the known keys pass.
func TestCollectCatalogsActionLabels(t *testing.T) {
	repoPath := t.TempDir()
	dir := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	info := `item_name: tool
catalog: base
installer: {type: msi, location: x.msi}
custom_action_labels: {install: Get, cancel: Stop, pending_install: Getting soon, uninstall: Drop}
`
	if err := os.WriteFile(filepath.Join(dir, "tool.yaml"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := CollectCatalogs(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0], `custom_action_labels: unknown key "uninstall"`) {
		t.Errorf("problems = %v, want one for the uninstall key", set.Problems)
	}
	if got := set.Catalogs["base"]["tool"].CustomActionLabels["install"]; got != "Get" {
		t.Errorf("install label = %q, want it carried into the catalog", got)
	}
}

// The agent refuses a nupkg without a hash and cannot tell whether one with
// neither a check block nor package_id is installed, so -check reports both
// when the catalog is built rather than on every agent.
func TestCollectCatalogsNupkgItem(t *testing.T) {
	tests := map[string]struct {
		info string
		want string // problem text; empty for none
	}{
		"complete": {
			info: "installer: {type: nupkg, location: a.nupkg, hash: abc, package_id: a}",
		},
		"check block in place of package_id": {
			info: "installer: {type: nupkg, location: a.nupkg, hash: abc}\ncheck: {registry: {name: A, version: '1.0'}}",
		},
		"installer without hash": {
			info: "installer: {type: nupkg, location: a.nupkg, package_id: a}",
			want: "nupkg installer has no hash",
		},
		"uninstaller location without hash": {
			info: "installer: {type: nupkg, location: a.nupkg, hash: abc, package_id: a}\nuninstaller: {type: nupkg, location: a.nupkg}",
			want: "nupkg uninstaller has a location but no hash",
		},
		"no check block and no package_id": {
			info: "installer: {type: nupkg, location: a.nupkg, hash: abc}",
			want: "nupkg item has no check block and no installer.package_id",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			repoPath := t.TempDir()
			dir := filepath.Join(repoPath, "packages-info")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "a.yaml")
			if err := os.WriteFile(path, []byte("item_name: a\ncatalog: base\n"+tc.info+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			set, err := CollectCatalogs(repoPath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(set.Problems) != 0 {
					t.Errorf("problems = %v, want none", set.Problems)
				}
				return
			}
			if len(set.Problems) != 1 || set.Problems[0] != path+": "+tc.want {
				t.Errorf("problems = %v, want [%s: %s]", set.Problems, path, tc.want)
			}
		})
	}
}

// A registry check sets one of name and name_regex, and a name_regex must
// compile: the agent fails the check of an item that breaks either rule, so
// -check reports it when the catalog is built.
func TestCollectCatalogsRegistryNameRegex(t *testing.T) {
	tests := map[string]struct {
		check string
		want  string // problem text; empty for none
	}{
		"name": {check: "{name: App, version: '1.0'}"},
		"name_regex": {
			check: `{name_regex: 'App [\d.]+', version: '1.0'}`,
		},
		"both": {
			check: `{name: App, name_regex: 'App [\d.]+', version: '1.0'}`,
			want:  `item "a": check.registry sets both name "App" and name_regex "App [\\d.]+"; set one`,
		},
		"does not compile": {
			check: `{name_regex: 'App (', version: '1.0'}`,
			want:  `item "a": check.registry name_regex "App (" does not compile`,
		},
		// Wrapped in the anchors, this one would compile and match anything.
		"escapes the anchors": {
			check: `{name_regex: 'x)|(.*', version: '1.0'}`,
			want:  `item "a": check.registry name_regex "x)|(.*" does not compile`,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			repoPath := t.TempDir()
			dir := filepath.Join(repoPath, "packages-info")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			info := "item_name: a\ncatalog: base\ninstaller: {type: msi, location: a.msi}\ncheck: {registry: " + tc.check + "}\n"
			if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(info), 0o644); err != nil {
				t.Fatal(err)
			}
			set, err := CollectCatalogs(repoPath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(set.Problems) != 0 {
					t.Errorf("problems = %v, want none", set.Problems)
				}
				return
			}
			if len(set.Problems) != 1 || !strings.Contains(set.Problems[0], tc.want) {
				t.Errorf("problems = %v, want one containing %s", set.Problems, tc.want)
			}
		})
	}
}

// An icon the storefront could not show is found when the catalog is built,
// not on every device; a good one gets the hash agents cache it by.
func TestCollectCatalogsIcon(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n...."
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(png)))
	for name, tc := range map[string]struct {
		keys, problem, wantHash string
	}{
		"hash filled in":    {keys: "icon_name: tool.png", wantHash: sum},
		"hash kept":         {keys: "icon_name: sub/tool.png\nicon_hash: " + strings.ToUpper(sum), wantHash: strings.ToUpper(sum)},
		"wrong hash":        {keys: "icon_name: tool.png\nicon_hash: abc", problem: "icon_hash is not the sha256"},
		"missing file":      {keys: "icon_name: none.png", problem: `icon_name "none.png"`},
		"leaves icons":      {keys: "icon_name: ../packages-info/tool.yaml", problem: "is not a path under icons/"},
		"not an image":      {keys: "icon_name: notes.txt", problem: "is not a PNG or JPEG"},
		"too large":         {keys: "icon_name: huge.png", problem: "the limit is"},
		"no icon, no check": {},
	} {
		t.Run(name, func(t *testing.T) {
			repoPath := t.TempDir()
			for rel, body := range map[string]string{
				"packages-info/tool.yaml": "item_name: tool\ncatalog: base\ninstaller: {type: msi, location: x.msi}\n" + tc.keys + "\n",
				"icons/tool.png":          png,
				"icons/sub/tool.png":      png,
				"icons/notes.txt":         "not an image",
				"icons/huge.png":          png + strings.Repeat("x", catalog.MaxIconBytes),
			} {
				path := filepath.Join(repoPath, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			set, err := CollectCatalogs(repoPath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.problem == "" && len(set.Problems) != 0 {
				t.Errorf("problems = %v, want none", set.Problems)
			}
			if tc.problem != "" && (len(set.Problems) != 1 || !strings.Contains(set.Problems[0], tc.problem)) {
				t.Errorf("problems = %v, want one with %q", set.Problems, tc.problem)
			}
			if got := set.Catalogs["base"]["tool"].IconHash; tc.problem == "" && got != tc.wantHash {
				t.Errorf("icon_hash = %q, want %q", got, tc.wantHash)
			}
		})
	}
}
