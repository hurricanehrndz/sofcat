package admin

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// Template checks (zip-items guide, "makecatalogs -check"): one function per
// pass, each naming the placement, the OS and the template in every problem,
// so a failure in CI reads like the device's refusal would.

// checkFacts are what a check renders with on goos: the built-in samples
// under the table's check_facts.
func checkFacts(table harness.Table, goos string) conditions.Facts {
	facts := conditions.SampleFacts(goos)
	maps.Copy(facts, table.CheckFacts)
	return facts
}

// checkPlacementTemplates renders every placement file template in the
// table, for each OS the placement has a path on, with .Items built from the
// catalog items that target the placement, managed both ways. A template
// that fails is a problem; a repository without a usable table has none to
// check.
func checkPlacementTemplates(h repoHarnesses, catalogs map[string]map[string]catalog.Item) []string {
	if !h.present || h.problem != "" {
		return nil
	}
	var problems []string
	for _, name := range h.table.Names() {
		p := h.table.Placements[name]
		for _, goos := range []string{harness.Darwin, harness.Windows} {
			root, ok := p.Path(goos)
			if !ok {
				continue
			}
			facts := checkFacts(h.table, goos)
			switch {
			case p.Kind == harness.KindMarketplace && p.Marketplace != nil:
				ctx := render.FileContext{OS: goos, Scope: p.ScopeFor(goos), Placement: p.View(name, root)}
				items := targetingItems(catalogs, name, p, goos, root)
				for _, f := range p.Marketplace.Manifests() {
					for _, err := range renderBothWays(f, ctx, items, facts) {
						problems = append(problems, fmt.Sprintf("%s: placement %q: manifest_files %q on %s: %v", h.path, name, f.Path, goos, err))
					}
				}
			case p.Kind == harness.KindSettings && p.Template != "":
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
func (h repoHarnesses) renderSettings(name string, p harness.Placement, goos, root string, facts conditions.Facts, catalogs map[string]map[string]catalog.Item) []error {
	regSpec := h.table.Placements[p.Registers]
	regRoot, _ := regSpec.Path(goos)
	ctx := render.FileContext{
		OS:        goos,
		Scope:     p.ScopeFor(goos),
		Placement: p.View(name, root),
		Registers: &render.Registered{Name: p.Registers, Root: regRoot, Kind: regSpec.Kind},
	}
	items := targetingItems(catalogs, p.Registers, regSpec, goos, regRoot)
	return renderBothWays(harness.ManifestFile{Path: root, Template: p.Template}, ctx, items, facts)
}

// renderBothWays renders f's template, named after f.Path (a settings
// template is named "template"), for an output at f.Path, with items all
// managed and all optional, and returns the distinct errors. An entry
// without a template has none.
func renderBothWays(f harness.ManifestFile, ctx render.FileContext, items []render.Item, facts conditions.Facts) []error {
	if f.Template == "" {
		return nil
	}
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
func targetingItems(catalogs map[string]map[string]catalog.Item, name string, p harness.Placement, goos, root string) []render.Item {
	byName := map[string]render.Item{}
	for _, items := range catalogs {
		for itemName, item := range items {
			v, ok := item.Installer.Variants[name][goos]
			if !ok {
				continue
			}
			tree := itemName
			if p.Kind == harness.KindMarketplace {
				tree = path.Join("plugins", itemName)
			}
			sep := "/"
			if goos == harness.Windows {
				sep = `\`
			}
			byName[itemName] = render.Item{
				Name:       itemName,
				Version:    item.Version,
				Path:       strings.TrimRight(root, sep) + sep + strings.ReplaceAll(tree, "/", sep),
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
