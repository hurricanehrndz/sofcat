package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// pluginsDir is the directory under a marketplace root that holds one tree
// per plugin.
const pluginsDir = "plugins"

// The generated files have no receipts: sofcat owns them outright and
// rewrites them from the receipts after every change to the placement and
// once per run (RegenerateAll).

// marketplaceManifest is the generated marketplace.json. Field order is the
// key order in the file.
type marketplaceManifest struct {
	Name                                string              `json:"name"`
	Owner                               any                 `json:"owner"`
	Renames                             any                 `json:"renames,omitempty"`
	AllowCrossMarketplaceDependenciesOn any                 `json:"allowCrossMarketplaceDependenciesOn,omitempty"`
	Plugins                             []marketplacePlugin `json:"plugins"`
}

type marketplacePlugin struct {
	Name           string `json:"name"`
	Source         string `json:"source"`
	Version        string `json:"version"`
	DefaultEnabled bool   `json:"defaultEnabled"`
}

// settingsDropIn is the generated settings file of a settings placement.
type settingsDropIn struct {
	ExtraKnownMarketplaces map[string]knownMarketplace `json:"extraKnownMarketplaces"`
	EnabledPlugins         map[string]bool             `json:"enabledPlugins"`
}

type knownMarketplace struct {
	Source marketplaceSource `json:"source"`
}

type marketplaceSource struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

// RegenerateAll regenerates every placement in the harness table that has
// generated files (generates) and a path on this OS, and returns a failed
// Result per placement that failed. A managed run calls it once, after scope
// exit, so a generated file deleted, refused or left stale by an earlier
// failure is restored even when no tree changes. A placement whose root does
// not exist is skipped: nothing was ever placed there, and an empty
// marketplace would only be clutter.
func (z *ZipInstaller) RegenerateAll() []Result {
	var failed []Result
	for _, name := range z.Harnesses.Names() {
		spec := z.Harnesses.Placements[name]
		if _, ok := spec.Path(z.Guard.GOOS); !ok || !z.generates(name, spec) {
			continue
		}
		root, err := z.resolve(name, spec)
		if err == nil {
			if _, statErr := os.Lstat(root); errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			err = z.regenerate(name)
		}
		if err != nil {
			failed = append(failed, fail(Target{Placement: name}, root, err))
		}
	}
	return failed
}

// generates reports whether placement name has generated files: a
// marketplace's manifests, or the file of a settings placement registering
// it.
func (z *ZipInstaller) generates(name string, spec harness.Placement) bool {
	return spec.Kind == harness.KindMarketplace || len(z.registrars(name)) > 0
}

// registrars returns the settings placements that register name, in table
// order.
func (z *ZipInstaller) registrars(name string) []string {
	var out []string
	for _, settings := range z.Harnesses.Names() {
		if s := z.Harnesses.Placements[settings]; s.Kind == harness.KindSettings && s.Registers == name {
			out = append(out, settings)
		}
	}
	return out
}

// regenerate rewrites the generated files of placement name from its
// receipts: a marketplace's manifest files, then every settings placement
// that registers it. It does nothing for a placement without generated
// files. Every write passes the guard; a refused path or a failed render
// wraps placement.ErrRefused. Apply and Remove call it after each change, so
// the files are current at once, and RegenerateAll once per run, so a file
// deleted or refused on one run is restored on the next.
func (z *ZipInstaller) regenerate(name string) error {
	spec, ok := z.Harnesses.Placements[name]
	if !ok || !z.generates(name, spec) {
		return nil
	}
	root, err := z.resolve(name, spec)
	if err != nil {
		return err
	}
	all, err := receipt.List(z.ReceiptDir)
	if err != nil {
		return fmt.Errorf("placement %q: unable to read receipts: %w", name, err)
	}
	rcs := slices.DeleteFunc(all, func(rc receipt.Receipt) bool { return rc.Placement != name })

	var errs []error
	if spec.Kind == harness.KindMarketplace && spec.Marketplace != nil {
		if err := z.writeManifests(name, spec, root, rcs); err != nil {
			errs = append(errs, err)
		}
	}
	for _, settings := range z.registrars(name) {
		if err := z.writeSettings(settings, z.Harnesses.Placements[settings], name, spec, root, rcs); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// writeManifests writes each of the marketplace's manifest files under
// root: the built-in Claude-format catalog listing every plugin rcs
// records, or the entry's template rendered with those plugins as .Items.
// Every entry is rendered before any is written, so a failed render leaves
// all of them as they were.
func (z *ZipInstaller) writeManifests(name string, spec harness.Placement, root string, rcs []receipt.Receipt) error {
	m := *spec.Marketplace
	manifest := marketplaceManifest{
		Name:                                m.Name,
		Owner:                               render.JSONValue(m.Owner),
		Renames:                             render.JSONValue(m.Renames),
		AllowCrossMarketplaceDependenciesOn: render.JSONValue(m.AllowCrossMarketplaceDependenciesOn),
		Plugins:                             []marketplacePlugin{},
	}
	for _, rc := range rcs { // receipt.List sorts by item
		manifest.Plugins = append(manifest.Plugins, marketplacePlugin{
			Name:           rc.Item,
			Source:         "./" + pluginsDir + "/" + rc.Item,
			Version:        rc.Version,
			DefaultEnabled: rc.Managed,
		})
	}
	builtin, err := encodeJSON(manifest)
	if err != nil {
		return fmt.Errorf("placement %q: %w", name, err)
	}
	ctx := z.fileContext(name, spec, root, rcs)
	type file struct {
		path      string
		data      []byte
		templated bool
	}
	files := make([]file, 0, len(m.Manifests()))
	for _, f := range m.Manifests() {
		local := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(local) {
			return fmt.Errorf("%w: placement %q: manifest file %q is not a path inside the placement", placement.ErrRefused, name, f.Path)
		}
		path, err := z.Guard.Check(filepath.Join(root, local), harness.ScopeMachine)
		if err != nil {
			return fmt.Errorf("placement %q: %w", name, err)
		}
		data := builtin
		if f.Template != "" {
			if data, err = renderFile(f.Path, f.Template, ctx, z.Facts, f.Path); err != nil {
				return fmt.Errorf("%w: placement %q: %w", placement.ErrRefused, name, err)
			}
			if data == nil {
				return fmt.Errorf("%w: placement %q: manifest_files %q: the template rendered nothing", placement.ErrRefused, name, f.Path)
			}
		}
		files = append(files, file{path, data, f.Template != ""})
	}
	for _, f := range files {
		if err := writeFileAtomic(f.path, f.data, 0o644); err != nil {
			return fmt.Errorf("placement %q: %w", name, err)
		}
		slog.Info("Wrote marketplace manifest", "placement", name, "path", f.path, "plugins", len(manifest.Plugins), "template", f.templated)
	}
	return nil
}

// writeSettings writes settings placement name's file for the placement
// registered (spec, at root, with receipts rcs): its template rendered with
// the registered placement's receipts as .Items, removed when the output is
// empty, or without a template the built-in Claude Code drop-in registering
// the marketplace and enabling its managed plugins, removed with no plugin
// left.
func (z *ZipInstaller) writeSettings(name string, spec harness.Placement, registered string, regSpec harness.Placement, root string, rcs []receipt.Receipt) error {
	if _, ok := spec.Path(z.Guard.GOOS); !ok {
		return nil
	}
	path, err := z.resolve(name, spec)
	if err != nil {
		return err
	}
	var data []byte
	if spec.Template != "" {
		ctx := z.fileContext(name, spec, path, rcs)
		ctx.Registers = &render.Registered{Name: registered, Root: root, Kind: regSpec.Kind}
		if data, err = renderFile("template", spec.Template, ctx, z.Facts, path); err != nil {
			return fmt.Errorf("%w: settings placement %q: %w", placement.ErrRefused, name, err)
		}
	} else if len(rcs) > 0 {
		if data, err = dropIn(regSpec.Marketplace.Name, root, rcs); err != nil {
			return fmt.Errorf("settings placement %q: %w", name, err)
		}
	}
	if data == nil {
		if err = os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("settings placement %q: %w", name, err)
		}
		slog.Info("Removed settings file: nothing to register", "placement", name, "path", path)
		return nil
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("settings placement %q: %w", name, err)
	}
	slog.Info("Wrote settings file", "placement", name, "path", path, "items", len(rcs), "template", spec.Template != "")
	return nil
}

// dropIn is the built-in Claude Code drop-in registering the marketplace at
// root under market and enabling its managed plugins.
func dropIn(market, root string, rcs []receipt.Receipt) ([]byte, error) {
	d := settingsDropIn{
		ExtraKnownMarketplaces: map[string]knownMarketplace{
			market: {Source: marketplaceSource{Source: "directory", Path: root}},
		},
		EnabledPlugins: map[string]bool{},
	}
	for _, rc := range rcs {
		if rc.Managed {
			d.EnabledPlugins[rc.Item+"@"+market] = true
		}
	}
	return encodeJSON(d)
}

// fileContext is what a placement file template of placement name sees:
// the placement at root and rcs as its items (zip-items guide, "Templates
// for placement files").
func (z *ZipInstaller) fileContext(name string, spec harness.Placement, root string, rcs []receipt.Receipt) render.FileContext {
	items := make([]render.Item, len(rcs))
	for i, rc := range rcs {
		items[i] = render.Item{Name: rc.Item, Version: rc.Version, Managed: rc.Managed, Path: rc.Path, TreeHash: rc.TreeHash, PlacedHash: rc.PlacedHash}
	}
	return render.FileContext{
		OS:        z.Guard.GOOS,
		Scope:     spec.ScopeFor(z.Guard.GOOS),
		Placement: spec.View(name, root),
		Items:     items,
	}
}

// renderFile parses and renders a placement file template for path.
func renderFile(name, source string, ctx render.FileContext, facts conditions.Facts, path string) ([]byte, error) {
	t, err := render.Parse(name, source)
	if err != nil {
		return nil, err
	}
	data, _, err := t.Render(ctx, facts, path)
	return data, err
}

// encodeJSON is v as indented JSON with a trailing newline. HTML characters
// stay as they are: harnesses read the file, not browsers.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// writeFileAtomic writes data to path with mode through a temporary file in
// the same directory, renamed into place, so a harness never reads a partial
// file. The directory is created if missing.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sofcat-*.tmp")
	if err != nil {
		return err
	}
	// Harnesses run as users; CreateTemp's 0600 would hide the file off
	// Windows, where no inherited ACL applies.
	err = tmp.Chmod(mode)
	if err == nil {
		_, err = tmp.Write(data)
	}
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
