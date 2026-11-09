package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// A placement's files have no receipts: sofcat owns them outright and
// rewrites them from the receipts after every change to the placement and
// once per run (RegenerateAll). Each is rendered from its entry's template.

// RegenerateAll regenerates every placement in the placement table that has
// files and a path on this OS, and returns a failed Result per placement
// that failed. A managed run calls it once, after scope exit, so a file
// deleted, refused or left stale by an earlier failure is restored even
// when no tree changes. A placement whose root does not exist is skipped:
// nothing was ever placed there, and an empty catalog would only be clutter.
func (z *ZipInstaller) RegenerateAll() []Result {
	return z.Regenerate(z.Table.Names())
}

// Regenerate is RegenerateAll for the placements in names only. The per-user
// helper passes those its user holds receipts for, since a user-scope root
// such as a tool's profile directory exists whether or not sofcat placed
// anything there.
func (z *ZipInstaller) Regenerate(names []string) []Result {
	var failed []Result
	for _, name := range names {
		spec := z.Table.Placements[name]
		if _, ok := spec.Path(z.Guard.GOOS); !ok || !generates(spec) {
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

// generates reports whether a placement has files to render.
func generates(spec placement.Spec) bool {
	return len(spec.Files) > 0
}

// regenerate rewrites the files of placement name from its receipts. It
// does nothing for a placement without files. Every entry with a path on
// this OS is resolved and rendered first. One that is refused or fails to
// render keeps its file as it was and the rest are written, unless the
// placement sets files_atomic, when none is. Two entries naming one file
// stop them all. An empty render removes the file. Every write passes the
// guard; a refused path or a failed render wraps placement.ErrRefused, and
// the errors are joined. Apply and Remove call it after each change, so the
// files are current at once, and RegenerateAll once per run, so a file
// deleted or refused on one run is restored on the next.
func (z *ZipInstaller) regenerate(name string) error {
	spec, ok := z.Table.Placements[name]
	if !ok || !generates(spec) {
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
	ctx := z.fileContext(name, spec, root, rcs)

	type output struct {
		path string
		data []byte
	}
	outputs := make([]output, 0, len(spec.Files))
	var errs []error
	seen := map[string]bool{}
	for i, f := range spec.Files {
		path, ok, err := z.filePath(name, spec, f, root)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		// Two entries writing one file would overwrite each other every run.
		key := strings.ToLower(path)
		if seen[key] {
			return fmt.Errorf("%w: placement %q: %s is named by two files entries", placement.ErrRefused, name, path)
		}
		seen[key] = true
		data, err := renderFile(fmt.Sprintf("files[%d]", i), f.Template, ctx, z.Facts, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: placement %q: %w", placement.ErrRefused, name, err))
			continue
		}
		outputs = append(outputs, output{path, data})
	}
	if spec.FilesAtomic && len(errs) > 0 {
		return errors.Join(errs...)
	}
	for _, o := range outputs {
		if o.data == nil {
			if err := os.Remove(o.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("placement %q: %w", name, err))
				continue
			}
			slog.Info("Removed placement file: the template rendered nothing", "placement", name, "path", o.path)
			continue
		}
		if err := writeFileAtomic(o.path, o.data, 0o644); err != nil {
			errs = append(errs, fmt.Errorf("placement %q: %w", name, err))
			continue
		}
		slog.Info("Wrote placement file", "placement", name, "path", o.path, "items", len(rcs))
	}
	return errors.Join(errs...)
}

// filePath resolves and checks where files entry f of placement name is
// written on this OS: under root for a relative path, else its own path
// here. ok is false when the entry has no path on this OS.
func (z *ZipInstaller) filePath(name string, spec placement.Spec, f placement.File, root string) (path string, ok bool, err error) {
	scope := spec.ScopeFor(z.Guard.GOOS)
	if f.Path != "" {
		local := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(local) {
			return "", false, fmt.Errorf("%w: placement %q: file %q is not a path inside the placement", placement.ErrRefused, name, f.Path)
		}
		path, err = z.Guard.Check(filepath.Join(root, local), scope)
	} else {
		raw, has := f.Paths[z.Guard.GOOS]
		if !has || raw == "" {
			return "", false, nil
		}
		path, err = z.Guard.Resolve(raw, scope)
	}
	if err != nil {
		return "", false, fmt.Errorf("placement %q: %w", name, err)
	}
	// A path that resolves inside the root, through its own spelling or a
	// symlink, is held to the rules for a relative path, so it never lands
	// in an item tree or on sofcat's own entries.
	if rel, in := z.Guard.RelativeTo(path, root); in {
		if err := spec.CheckFile(rel); err != nil {
			return "", false, fmt.Errorf("%w: placement %q: %w", placement.ErrRefused, name, err)
		}
	}
	return path, true, nil
}

// fileContext is what a placement file template of placement name sees:
// the placement at root and rcs as its items (zip-items guide, "Templates
// for placement files").
func (z *ZipInstaller) fileContext(name string, spec placement.Spec, root string, rcs []receipt.Receipt) render.FileContext {
	items := make([]render.Item, len(rcs))
	for i, rc := range rcs {
		items[i] = render.Item{Name: rc.Item, Version: rc.Version, Managed: rc.Managed, Path: rc.Path, TreeHash: rc.TreeHash, PlacedHash: rc.PlacedHash}
	}
	ctx := render.FileContext{
		OS:        z.Guard.GOOS,
		Scope:     spec.ScopeFor(z.Guard.GOOS),
		Placement: spec.View(name, root),
		Items:     items,
	}
	// As in tree templates: the service, in machine scope, has no user.
	if ctx.Scope == placement.ScopeUser {
		ctx.User = z.User
	}
	return ctx
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
