package admin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// Template checks (zip-items guide, "makecatalogs -check"): one function per
// pass, each naming the placement, the OS and the template in every problem,
// so a failure in CI reads like the device's refusal would.

// checkFacts are what a check renders with on goos: the built-in samples
// under the table's check_facts.
func checkFacts(table placement.Table, goos string) conditions.Facts {
	facts := conditions.SampleFacts(goos)
	maps.Copy(facts, table.CheckFacts)
	return facts
}

// checkPlacementTemplates renders every placement file template in the
// table, the defaults included, for each OS the placement has a path on,
// with .Items built from the catalog items that target the placement,
// managed both ways. A template that fails is a problem; a repository
// without a usable table has none to check.
func checkPlacementTemplates(h repoPlacements, catalogs map[string]map[string]catalog.Item) []string {
	if !h.present || h.problem != "" {
		return nil
	}
	var problems []string
	for _, name := range h.table.Names() {
		p := h.table.Placements[name]
		for _, goos := range []string{placement.Darwin, placement.Windows} {
			root, ok := p.Path(goos)
			if !ok {
				continue
			}
			facts := checkFacts(h.table, goos)
			switch {
			case p.Kind == placement.KindMarketplace && p.Marketplace != nil:
				ctx := render.FileContext{OS: goos, Scope: p.ScopeFor(goos), Placement: p.View(name, root)}
				items := targetingItems(catalogs, name, p, goos, root)
				for _, f := range p.Marketplace.Manifests() {
					for _, err := range renderBothWays(f, ctx, items, facts) {
						problems = append(problems, fmt.Sprintf("%s: placement %q: manifest_files %q on %s: %v", h.path, name, f.Path, goos, err))
					}
				}
			case p.Kind == placement.KindSettings:
				for _, err := range h.renderSettings(name, p, goos, root, facts, catalogs) {
					problems = append(problems, fmt.Sprintf("%s: placement %q: template on %s: %v", h.path, name, goos, err))
				}
			}
		}
	}
	return problems
}

// renderSettings renders settings placement name's template on goos with the
// registered placement's items.
func (h repoPlacements) renderSettings(name string, p placement.Spec, goos, root string, facts conditions.Facts, catalogs map[string]map[string]catalog.Item) []error {
	regSpec := h.table.Placements[p.Registers]
	regRoot, _ := regSpec.Path(goos)
	ctx := render.FileContext{
		OS:        goos,
		Scope:     p.ScopeFor(goos),
		Placement: p.View(name, root),
		Registers: regSpec.Registered(p.Registers, regRoot),
	}
	items := targetingItems(catalogs, p.Registers, regSpec, goos, regRoot)
	return renderBothWays(placement.ManifestFile{Path: root, Template: p.SettingsTemplate()}, ctx, items, facts)
}

// renderBothWays renders f's template, named after f.Path (a settings
// template is named "template"), for an output at f.Path, with items all
// managed and all optional, and returns the distinct errors.
func renderBothWays(f placement.ManifestFile, ctx render.FileContext, items []render.Item, facts conditions.Facts) []error {
	name := f.Path
	if ctx.Registers != nil {
		name = "template"
	}
	t, err := render.Parse(name, f.Template)
	if err != nil {
		return []error{err}
	}
	var errs []error
	seen := map[string]bool{}
	for _, managed := range []bool{true, false} {
		ctx.Items = slices.Clone(items)
		for i := range ctx.Items {
			ctx.Items[i].Managed = managed
		}
		if _, _, err := t.Render(ctx, facts, f.Path); err != nil && !seen[err.Error()] {
			seen[err.Error()] = true
			errs = append(errs, fmt.Errorf("managed %v: %w", managed, err))
		}
	}
	return errs
}

// targetingItems lists, sorted by name, the catalog items with a variant for
// placement name on goos, as the receipts would describe them once placed
// under root, with nothing rendered.
func targetingItems(catalogs map[string]map[string]catalog.Item, name string, p placement.Spec, goos, root string) []render.Item {
	byName := map[string]render.Item{}
	for _, items := range catalogs {
		for itemName, item := range items {
			v, ok := item.Installer.Variants[name][goos]
			if !ok {
				continue
			}
			byName[itemName] = render.Item{
				Name:       itemName,
				Version:    item.Version,
				Path:       placedPath(root, goos, treePath(p, itemName)),
				TreeHash:   v.TreeHash,
				PlacedHash: v.TreeHash,
			}
		}
	}
	names := slices.Sorted(maps.Keys(byName))
	out := make([]render.Item, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out
}

// SampleUser is the .User a check renders a user-scope tree template with.
const SampleUser = "jdoe"

// CheckTreeTemplates renders templates, template path to source, as sofcat
// would inside item's tree in placement name on goos (render.TreeContext,
// zip-items guide "Tree templates"): managed both ways, SampleUser in user
// scope and the check facts. item's Managed and Path are set here. It
// returns the distinct errors, each naming the template.
func CheckTreeTemplates(table placement.Table, name, goos string, item render.TreeItem, templates map[string]string) []error {
	spec := table.Placements[name]
	root, _ := spec.Path(goos)
	item.Path = placedPath(root, goos, treePath(spec, item.Name))
	ctx := render.TreeContext{OS: goos, Scope: spec.ScopeFor(goos), Placement: spec.View(name, root), Item: item}
	if ctx.Scope == placement.ScopeUser {
		ctx.User = SampleUser
	}
	facts := checkFacts(table, goos)
	var errs []error
	for _, p := range slices.Sorted(maps.Keys(templates)) {
		t, err := render.Parse(p, templates[p])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		seen := map[string]bool{}
		for _, managed := range []bool{true, false} {
			ctx.Item.Managed = managed
			if _, _, err := t.Render(ctx, facts, strings.TrimSuffix(p, artifact.TemplateSuffix)); err != nil && !seen[err.Error()] {
				seen[err.Error()] = true
				errs = append(errs, fmt.Errorf("managed %v: %w", managed, err))
			}
		}
	}
	return errs
}

// checkZipTemplates opens every zip the catalogs' zip items name that is in
// the repository and renders its templates, per variant, as
// CheckTreeTemplates does. A zip the repository does not hold (one hosted
// elsewhere) is not checked; one that cannot be read, or that ships
// template names sofcat refuses (artifact.ShippedTemplates), is a problem.
func checkZipTemplates(repoPath string, h repoPlacements, catalogs map[string]map[string]catalog.Item) []string {
	if !h.present || h.problem != "" {
		return nil
	}
	var problems []string
	for _, catName := range slices.Sorted(maps.Keys(catalogs)) {
		items := catalogs[catName]
		for _, itemName := range slices.Sorted(maps.Keys(items)) {
			item := items[itemName]
			if item.Installer.Type != "zip" {
				continue
			}
			for _, name := range slices.Sorted(maps.Keys(item.Installer.Variants)) {
				if _, ok := h.table.Placements[name]; !ok {
					continue // checkZipItem reports it
				}
				variants := item.Installer.Variants[name]
				for _, goos := range slices.Sorted(maps.Keys(variants)) {
					ti := render.TreeItem{Name: itemName, Version: item.Version, TreeHash: variants[goos].TreeHash}
					for _, err := range checkZipVariant(repoPath, h.table, name, goos, ti, variants[goos].Location) {
						problems = append(problems, fmt.Sprintf("%s: zip item %q, placement %q on %s: %v", variants[goos].Location, itemName, name, goos, err))
					}
				}
			}
		}
	}
	return problems
}

// checkZipVariant renders the templates in the zip at location, item's tree
// for placement name on goos, and returns the errors. A zip the repository
// does not hold is not checked.
func checkZipVariant(repoPath string, table placement.Table, name, goos string, item render.TreeItem, location string) []error {
	local := filepath.FromSlash(location)
	if !filepath.IsLocal(local) {
		return []error{errors.New("location is not a path inside the repo")}
	}
	templates, err := zipTemplates(filepath.Join(repoPath, local))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{err}
	}
	return CheckTreeTemplates(table, name, goos, item, templates)
}

// zipTemplates reads the templates among the zip's entries, path to source.
func zipTemplates(path string) (map[string]string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	byName := make(map[string]*zip.File, len(r.File))
	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		byName[f.Name] = f
		names = append(names, f.Name)
	}
	templates, err := artifact.ShippedTemplates(names)
	if err != nil {
		return nil, err
	}
	sources := make(map[string]string, len(templates))
	for _, p := range templates {
		rc, err := byName[p].Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		// Parse rejects a template over the cap; one byte more is enough to
		// show it.
		body, err := io.ReadAll(io.LimitReader(rc, render.MaxTemplate+1))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		sources[p] = string(body)
	}
	return sources, nil
}

// treePath is item's tree relative to its placement root: plugins/<item> in
// a marketplace, <item> elsewhere.
func treePath(p placement.Spec, item string) string {
	if p.Kind == placement.KindMarketplace {
		return path.Join("plugins", item)
	}
	return item
}

// placedPath joins root and tree with goos's separator, as a receipt on
// that OS would record the tree's path.
func placedPath(root, goos, tree string) string {
	sep := "/"
	if goos == placement.Windows {
		sep = `\`
	}
	return strings.TrimRight(root, sep) + sep + strings.ReplaceAll(tree, "/", sep)
}
