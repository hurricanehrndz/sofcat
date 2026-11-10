// Package admin holds repository-side tooling. It must stay free of agent
// dependencies (pkg/config, pkg/download) so cmd/makecatalogs cross-builds
// for any platform.
package admin

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"go.yaml.in/yaml/v4"
)

// PackageInfo is a packages-info file: one catalog item and the catalog it
// belongs to.
type PackageInfo struct {
	ItemName string       `yaml:"item_name"`
	Catalog  string       `yaml:"catalog"`
	Item     catalog.Item `yaml:",inline"`
}

// CatalogSet is the result of compiling a repo's package-info files.
type CatalogSet struct {
	// Catalogs maps catalog name to item name to item.
	Catalogs map[string]map[string]catalog.Item
	// Problems lists package-info files that were skipped or overridden, zip
	// items with unusable variants, nupkg items the agent would refuse or
	// cannot check, an invalid placements.yaml, and manifests
	// whose conditional_items conditions do not compile.
	// A normal makecatalogs run warns about them and carries on; --check fails on them.
	Problems []string
}

// Names returns the catalog names in sorted order.
func (s CatalogSet) Names() []string {
	names := make([]string, 0, len(s.Catalogs))
	for name := range s.Catalogs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CollectCatalogs reads <repo>/packages-info and compiles the catalogs it
// describes without writing anything. Unreadable or unparsable files are
// errors; skipped or duplicated items, and invalid conditions in package-info
// files and in <repo>/manifests, are reported in Problems.
func CollectCatalogs(repoPath string) (CatalogSet, error) { //nolint:gocognit // CEILING: known-complex, split in Workstream B (kernel-repair)
	packagesInfoPath := filepath.Join(repoPath, "packages-info")

	if _, err := os.Stat(packagesInfoPath); err != nil {
		return CatalogSet{}, fmt.Errorf("packages-info path unavailable: %w", err)
	}

	var packageInfoQueue []string
	err := filepath.WalkDir(packagesInfoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Skip dotfiles such as macOS AppleDouble `._foo.yaml`, as Munki does.
		if strings.HasPrefix(d.Name(), ".") && path != packagesInfoPath {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
			slog.Debug("Queuing package-info file", "path", path)
			packageInfoQueue = append(packageInfoQueue, path)
		}
		return nil
	})
	if err != nil {
		return CatalogSet{}, err
	}

	set := CatalogSet{Catalogs: make(map[string]map[string]catalog.Item)}
	placements := readPlacements(repoPath)
	if placements.problem != "" {
		set.Problems = append(set.Problems, placements.problem)
	}
	// source records which package-info file each catalog item came from.
	source := make(map[string]map[string]string)
	// folded maps a lowercased catalog name to the first spelling seen, since
	// macOS and Windows file systems treat Prod.yaml and prod.yaml as one file.
	folded := make(map[string]string)
	for _, packageInfoPath := range packageInfoQueue {
		yamlFile, err := os.ReadFile(packageInfoPath)
		if err != nil {
			return CatalogSet{}, fmt.Errorf("read package-info %s: %w", packageInfoPath, err)
		}

		var parsed PackageInfo
		if err = yaml.Unmarshal(yamlFile, &parsed); err != nil {
			return CatalogSet{}, fmt.Errorf("parse package-info %s: %w", packageInfoPath, err)
		}
		if parsed.Catalog == "" {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: no catalog; skipped", packageInfoPath))
			continue
		}
		// The catalog name becomes a file name under catalogs/, so it must not
		// carry a path.
		if strings.ContainsAny(parsed.Catalog, `/\:`) || parsed.Catalog == "." || parsed.Catalog == ".." {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: invalid catalog name %q; skipped", packageInfoPath, parsed.Catalog))
			continue
		}
		if first, ok := folded[strings.ToLower(parsed.Catalog)]; ok && first != parsed.Catalog {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: catalog %q differs from %q only by case; skipped", packageInfoPath, parsed.Catalog, first))
			continue
		}
		folded[strings.ToLower(parsed.Catalog)] = parsed.Catalog

		itemName := strings.TrimSpace(parsed.ItemName)
		if itemName == "" {
			itemName = strings.TrimSpace(strings.ReplaceAll(parsed.Item.DisplayName, " ", ""))
		}
		if itemName == "" {
			itemName = strings.TrimSuffix(filepath.Base(packageInfoPath), filepath.Ext(packageInfoPath))
		}
		if itemName == "" {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: no item_name/display_name; skipped", packageInfoPath))
			continue
		}

		if set.Catalogs[parsed.Catalog] == nil {
			set.Catalogs[parsed.Catalog] = map[string]catalog.Item{}
			source[parsed.Catalog] = map[string]string{}
		}
		if prev, ok := source[parsed.Catalog][itemName]; ok {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: duplicate item %q in catalog %q; replaces %s", packageInfoPath, itemName, parsed.Catalog, prev))
		}
		// Kept in the catalog: agents treat an invalid condition as false.
		if err := conditions.Check(parsed.Item.InstallableCondition); err != nil {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: invalid installable_condition: %v", packageInfoPath, err))
		}
		set.Problems = append(set.Problems, checkDependsCheck(packageInfoPath, parsed.Item.DependsCheck)...)
		set.Problems = append(set.Problems, checkActionLabels(packageInfoPath, parsed.Item.CustomActionLabels)...)
		if parsed.Item.Installer.Type == "zip" {
			set.Problems = append(set.Problems, checkZipItem(packageInfoPath, parsed.Item.Installer, placements)...)
		}
		set.Problems = append(set.Problems, checkNupkgItem(packageInfoPath, parsed.Item)...)
		set.Catalogs[parsed.Catalog][itemName] = parsed.Item
		source[parsed.Catalog][itemName] = packageInfoPath
	}

	// Writing would replace catalogs/ with nothing; refuse rather than publish
	// an empty repo.
	if len(set.Catalogs) == 0 {
		return CatalogSet{}, fmt.Errorf("no catalogs to write: no package-info file under %s names a valid catalog", packagesInfoPath)
	}

	set.Problems = append(set.Problems, checkPlacementTemplates(placements, set.Catalogs)...)
	set.Problems = append(set.Problems, checkZipTemplates(repoPath, placements, set.Catalogs)...)
	set.Problems = append(set.Problems, checkManifests(repoPath)...)
	return set, nil
}

// WriteCatalogs replaces <repo>/catalogs with one YAML file per catalog in set.
func WriteCatalogs(repoPath string, set CatalogSet) error {
	catalogsPath := filepath.Join(repoPath, "catalogs")

	// Marshal everything before touching the existing catalogs.
	rendered := make(map[string][]byte, len(set.Catalogs))
	for catalogName, catalogItems := range set.Catalogs {
		catalogYAML, err := yaml.Marshal(catalogItems)
		if err != nil {
			return fmt.Errorf("marshal catalog %s: %w", catalogName, err)
		}
		rendered[catalogName] = catalogYAML
	}

	// CEILING: not atomic. A write error after RemoveAll (disk full, file lock)
	// leaves catalogs/ partial; rerunning repairs it. Upgrade path: write to a
	// sibling temp dir and rename it into place.
	if err := os.RemoveAll(catalogsPath); err != nil {
		return fmt.Errorf("clean catalogs path %s: %w", catalogsPath, err)
	}
	if err := os.MkdirAll(catalogsPath, 0o755); err != nil {
		return fmt.Errorf("create catalogs path %s: %w", catalogsPath, err)
	}
	for catalogName, catalogYAML := range rendered {
		catalogPath := filepath.Join(catalogsPath, catalogName+".yaml")
		if err := os.WriteFile(catalogPath, catalogYAML, 0o644); err != nil {
			return fmt.Errorf("write catalog %s: %w", catalogPath, err)
		}
	}

	return nil
}

// repoPlacements is what CollectCatalogs read from <repo>/placements.yaml.
type repoPlacements struct {
	path    string // the file, for problems
	table   placement.Table
	present bool   // the file exists
	problem string // why it cannot be used; empty when it is valid or absent
}

// readPlacements reads and validates <repo>/placements.yaml, with its template
// files read from the repo. A repo without one is fine until it has a zip item.
func readPlacements(repoPath string) repoPlacements {
	path := filepath.Join(repoPath, "placements.yaml")
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return repoPlacements{}
	}
	if err != nil {
		return repoPlacements{path: path, present: true, problem: fmt.Sprintf("%s: %v", path, err)}
	}
	table, err := placement.Parse(body, func(rel string) ([]byte, error) {
		return fs.ReadFile(os.DirFS(repoPath), rel)
	})
	if err != nil {
		return repoPlacements{path: path, present: true, problem: fmt.Sprintf("%s: %v", path, err)}
	}
	return repoPlacements{path: path, table: table, present: true}
}

// checkDependsCheck reports depends_check entries without a display_name,
// which the deferral message needs, with no check at all, or with a script
// no OS would run: a key other than windows and darwin, an empty script, or a
// darwin script without the #! line it is executed by.
func checkDependsCheck(path string, deps []catalog.DependsCheck) []string {
	var problems []string
	for i, dep := range deps {
		if strings.TrimSpace(dep.DisplayName) == "" {
			problems = append(problems, fmt.Sprintf("%s: depends_check[%d] has no display_name", path, i))
		}
		if dep.Sofcat == "" && dep.Munki == "" && len(dep.Script) == 0 {
			problems = append(problems, fmt.Sprintf("%s: depends_check[%d] needs sofcat, munki or script", path, i))
		}
		for _, goos := range slices.Sorted(maps.Keys(dep.Script)) {
			script := dep.Script[goos]
			switch {
			case !slices.Contains(catalog.DependsCheckScriptOS, goos):
				problems = append(problems, fmt.Sprintf("%s: depends_check[%d] script key %q is not one of %s", path, i, goos, strings.Join(catalog.DependsCheckScriptOS, ", ")))
			case strings.TrimSpace(script) == "":
				problems = append(problems, fmt.Sprintf("%s: depends_check[%d] script for %s is empty", path, i, goos))
			case goos == "darwin" && !strings.HasPrefix(script, "#!"):
				problems = append(problems, fmt.Sprintf("%s: depends_check[%d] script for darwin does not start with #!", path, i))
			}
		}
	}
	return problems
}

// checkActionLabels reports custom_action_labels keys the storefront does
// not know, which it would silently ignore.
func checkActionLabels(path string, labels map[string]string) []string {
	var problems []string
	for _, key := range slices.Sorted(maps.Keys(labels)) {
		if !slices.Contains(catalog.ActionLabelKeys, key) {
			problems = append(problems, fmt.Sprintf("%s: custom_action_labels: unknown key %q", path, key))
		}
	}
	return problems
}

// checkZipItem reports what makes a zip installer unusable: no variants, a
// variant without location, hash or tree_hash, an OS other than darwin or
// windows, or a placement that placements.yaml does not define.
func checkZipItem(path string, inst catalog.InstallerItem, h repoPlacements) []string {
	var problems []string
	if len(inst.Variants) == 0 {
		problems = append(problems, fmt.Sprintf("%s: zip installer has no variants", path))
	}
	if !h.present {
		problems = append(problems, fmt.Sprintf("%s: zip item, but the repo has no placements.yaml", path))
	}
	placements := make([]string, 0, len(inst.Variants))
	for name := range inst.Variants {
		placements = append(placements, name)
	}
	sort.Strings(placements)
	for _, name := range placements {
		// An invalid table is reported once on its own; checking names
		// against it would only add noise.
		if _, ok := h.table.Placements[name]; h.present && h.problem == "" && !ok {
			problems = append(problems, fmt.Sprintf("%s: variant placement %q is not in placements.yaml", path, name))
		}
		for goos, v := range inst.Variants[name] {
			if goos != placement.Darwin && goos != placement.Windows {
				problems = append(problems, fmt.Sprintf("%s: variant %s.%s: OS must be darwin or windows", path, name, goos))
			}
			if v.Location == "" || v.Hash == "" || v.TreeHash == "" {
				problems = append(problems, fmt.Sprintf("%s: variant %s.%s needs location, hash and tree_hash", path, name, goos))
			}
		}
	}
	return problems
}

// checkNupkgItem reports what the agent would refuse or could not check in a
// nupkg item: an installer without hash, an uninstaller with a location but
// no hash, and no check block and no installer.package_id, which leaves the
// agent no way to tell whether the package is installed.
func checkNupkgItem(path string, item catalog.Item) []string {
	var problems []string
	if item.Installer.Type == "nupkg" && item.Installer.Hash == "" {
		problems = append(problems, fmt.Sprintf("%s: nupkg installer has no hash", path))
	}
	if item.Uninstaller.Type == "nupkg" && item.Uninstaller.Location != "" && item.Uninstaller.Hash == "" {
		problems = append(problems, fmt.Sprintf("%s: nupkg uninstaller has a location but no hash", path))
	}
	check := item.Check
	hasCheck := check.Script != "" || len(check.File) > 0 || check.Registry.Version != "" || check.Appx.Name != ""
	if item.Installer.Type == "nupkg" && !hasCheck && item.Installer.PackageID == "" {
		problems = append(problems, fmt.Sprintf("%s: nupkg item has no check block and no installer.package_id", path))
	}
	return problems
}

// conditionalBlock is the part of a manifest's conditional_items that
// checkManifests reads. pkg/manifest owns the full shape, but it pulls in the
// agent's config and download, which this package must not.
type conditionalBlock struct {
	Condition        string             `yaml:"condition"`
	ConditionalItems []conditionalBlock `yaml:"conditional_items"`
}

// checkManifests reports every conditional_items condition, nested ones
// included, in <repo>/manifests that has a syntax error, and every manifest it
// cannot read. A repo without manifests has nothing to check.
func checkManifests(repoPath string) []string {
	var problems []string
	var check func(path string, blocks []conditionalBlock)
	check = func(path string, blocks []conditionalBlock) {
		for _, b := range blocks {
			if err := conditions.Check(b.Condition); err != nil {
				problems = append(problems, fmt.Sprintf("%s: invalid conditional_items condition %q: %v", path, b.Condition, err))
			}
			check(path, b.ConditionalItems)
		}
	}

	manifestsPath := filepath.Join(repoPath, "manifests")
	var manifestQueue []string
	err := filepath.WalkDir(manifestsPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasPrefix(d.Name(), ".") && path != manifestsPath {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(path)); !d.IsDir() && (ext == ".yaml" || ext == ".yml") {
			manifestQueue = append(manifestQueue, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		problems = append(problems, fmt.Sprintf("%s: %v", manifestsPath, err))
	}

	for _, path := range manifestQueue {
		body, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		var m struct {
			ConditionalItems []conditionalBlock `yaml:"conditional_items"`
		}
		if err := yaml.Unmarshal(body, &m); err != nil {
			problems = append(problems, fmt.Sprintf("%s: parse manifest: %v", path, err))
			continue
		}
		check(path, m.ConditionalItems)
	}
	return problems
}
