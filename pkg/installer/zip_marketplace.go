package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// pluginsDir is the directory under a marketplace root that holds one tree
// per plugin.
const pluginsDir = "plugins"

// The generated files have no receipts: sofcat owns them outright and
// rewrites them from the receipts after every change to the placement and
// once per run (RegenerateAll). Each is rendered from a template: the
// entry's or the placement's own, else placement.DefaultCatalogTemplate and
// placement.DefaultSettingsTemplate.

// RegenerateAll regenerates every placement in the placement table that has
// generated files (generates) and a path on this OS, and returns a failed
// Result per placement that failed. A managed run calls it once, after scope
// exit, so a generated file deleted, refused or left stale by an earlier
// failure is restored even when no tree changes. A placement whose root does
// not exist is skipped: nothing was ever placed there, and an empty
// marketplace would only be clutter.
func (z *ZipInstaller) RegenerateAll() []Result {
	var failed []Result
	for _, name := range z.Table.Names() {
		spec := z.Table.Placements[name]
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
func (z *ZipInstaller) generates(name string, spec placement.Spec) bool {
	return spec.Kind == placement.KindMarketplace || len(z.registrars(name)) > 0
}

// registrars returns the settings placements that register name, in table
// order.
func (z *ZipInstaller) registrars(name string) []string {
	var out []string
	for _, settings := range z.Table.Names() {
		if s := z.Table.Placements[settings]; s.Kind == placement.KindSettings && s.Registers == name {
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
	spec, ok := z.Table.Placements[name]
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
	if spec.Kind == placement.KindMarketplace && spec.Marketplace != nil {
		if err := z.writeManifests(name, spec, root, rcs); err != nil {
			errs = append(errs, err)
		}
	}
	for _, settings := range z.registrars(name) {
		if err := z.writeSettings(settings, z.Table.Placements[settings], name, spec, root, rcs); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// writeManifests writes each of the marketplace's manifest files under
// root: the entry's template rendered with the plugins rcs records as
// .Items. Every entry is rendered before any is written, so a failed render
// leaves all of them as they were.
func (z *ZipInstaller) writeManifests(name string, spec placement.Spec, root string, rcs []receipt.Receipt) error {
	ctx := z.fileContext(name, spec, root, rcs)
	type file struct {
		path string
		data []byte
	}
	manifests := spec.Marketplace.Manifests()
	files := make([]file, 0, len(manifests))
	for _, f := range manifests {
		local := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(local) {
			return fmt.Errorf("%w: placement %q: manifest file %q is not a path inside the placement", placement.ErrRefused, name, f.Path)
		}
		path, err := z.Guard.Check(filepath.Join(root, local), placement.ScopeMachine)
		if err != nil {
			return fmt.Errorf("placement %q: %w", name, err)
		}
		data, err := renderFile(f.Path, f.Template, ctx, z.Facts, f.Path)
		if err != nil {
			return fmt.Errorf("%w: placement %q: %w", placement.ErrRefused, name, err)
		}
		if data == nil {
			return fmt.Errorf("%w: placement %q: manifest_files %q: the template rendered nothing", placement.ErrRefused, name, f.Path)
		}
		files = append(files, file{path, data})
	}
	for _, f := range files {
		if err := writeFileAtomic(f.path, f.data, 0o644); err != nil {
			return fmt.Errorf("placement %q: %w", name, err)
		}
		slog.Info("Wrote marketplace manifest", "placement", name, "path", f.path, "plugins", len(rcs))
	}
	return nil
}

// writeSettings writes settings placement name's file for the placement
// registered (spec, at root, with receipts rcs): its template rendered with
// the registered placement's receipts as .Items, removed when the output is
// empty. The default template is the Claude Code drop-in registering the
// marketplace and enabling its managed plugins, empty with no plugin left.
func (z *ZipInstaller) writeSettings(name string, spec placement.Spec, registered string, regSpec placement.Spec, root string, rcs []receipt.Receipt) error {
	if _, ok := spec.Path(z.Guard.GOOS); !ok {
		return nil
	}
	path, err := z.resolve(name, spec)
	if err != nil {
		return err
	}
	ctx := z.fileContext(name, spec, path, rcs)
	ctx.Registers = regSpec.Registered(registered, root)
	data, err := renderFile("template", spec.SettingsTemplate(), ctx, z.Facts, path)
	if err != nil {
		return fmt.Errorf("%w: settings placement %q: %w", placement.ErrRefused, name, err)
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
	slog.Info("Wrote settings file", "placement", name, "path", path, "items", len(rcs))
	return nil
}

// fileContext is what a placement file template of placement name sees:
// the placement at root and rcs as its items (zip-items guide, "Templates
// for placement files").
func (z *ZipInstaller) fileContext(name string, spec placement.Spec, root string, rcs []receipt.Receipt) render.FileContext {
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
