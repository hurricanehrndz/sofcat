package admin

import (
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
  hash: abc
`
	itemB := `
display_name: Agent Tool
catalog: base
installer:
  type: nupkg
  location: packages/agent/agent.nupkg
  hash: def
`
	itemSkip := `
display_name: No Catalog
installer:
  type: nupkg
  location: packages/skip/skip.nupkg
  hash: ghi
`
	if err := os.WriteFile(filepath.Join(packagesInfoPath, "chrome.yaml"), []byte(itemA), 0o644); err != nil {
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

// A requires_software entry the agent cannot use is caught by -check: without
// display_name the deferral message has no name, and without munki or
// chocolatey no OS checks it.
func TestCollectCatalogsRequiresSoftware(t *testing.T) {
	repoPath := t.TempDir()
	dir := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	info := `item_name: tool
catalog: base
installer: {type: msi, location: x.msi}
requires_software:
  - {display_name: Demo Tool, chocolatey: demo-tool}
  - {display_name: Mac Tool, munki: MacTool}
  - {chocolatey: nameless}
  - {display_name: Nowhere}
`
	if err := os.WriteFile(filepath.Join(dir, "tool.yaml"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := CollectCatalogs(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"requires_software[2] has no display_name", "requires_software[3] needs munki or chocolatey"}
	if len(set.Problems) != len(want) {
		t.Fatalf("problems = %v, want %v", set.Problems, want)
	}
	for i, w := range want {
		if !strings.Contains(set.Problems[i], w) {
			t.Errorf("problem %d = %q, want it to mention %q", i, set.Problems[i], w)
		}
	}
	if got := set.Catalogs["base"]["tool"].RequiresSoftware; len(got) != 4 || got[0].Chocolatey != "demo-tool" {
		t.Errorf("requires_software = %+v, want it carried into the catalog", got)
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
