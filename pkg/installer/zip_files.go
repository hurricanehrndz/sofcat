package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/jsonmerge"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/render"
)

// A placement's files have no receipts: sofcat owns them outright and
// rewrites them from the receipts after every change to the placement and
// once per run (RegenerateAll for machine scope, Regenerate from the per-user
// helper for user scope). Each is rendered from its entry's template.

// RegenerateAll regenerates every machine-scope placement in the placement
// table that has files and a path on this OS, or is rootless, and returns a
// failed Result per placement that failed. A managed run calls it once,
// after scope exit, so a file deleted, refused or left stale by an earlier
// failure is restored even when no tree changes. A placement whose root does
// not exist is skipped: nothing was ever placed there, and an empty catalog
// would only be clutter. A rootless placement has no root and is always
// regenerated. A user-scope placement is skipped too: the service has no
// user to expand its root for, and the per-user helper regenerates it.
func (z *ZipInstaller) RegenerateAll() []Result {
	names := slices.DeleteFunc(z.Table.Names(), func(name string) bool {
		return z.Table.Placements[name].ScopeFor(z.Guard.GOOS) == placement.ScopeUser
	})
	failed, _ := z.Regenerate(names)
	return failed
}

// Regenerate is RegenerateAll for the placements in names only, whatever
// their scope. The per-user helper passes the user-scope placements its user
// holds receipts for, since a user-scope root such as a tool's profile
// directory exists whether or not sofcat placed anything there. skipped names
// the placements it left alone for want of a root.
func (z *ZipInstaller) Regenerate(names []string) (failed []Result, skipped []string) {
	for _, name := range names {
		spec := z.Table.Placements[name]
		if !generates(spec) {
			continue
		}
		if spec.Rootless() {
			if err := z.regenerate(name); err != nil {
				failed = append(failed, fail(Target{Placement: name}, "", err))
			}
			continue
		}
		if _, ok := spec.Path(z.Guard.GOOS); !ok {
			continue
		}
		root, err := z.resolve(name, spec)
		if err == nil {
			if _, statErr := os.Lstat(root); errors.Is(statErr, fs.ErrNotExist) {
				skipped = append(skipped, name)
				continue
			}
			err = z.regenerate(name)
		}
		if err != nil {
			failed = append(failed, fail(Target{Placement: name}, root, err))
		}
	}
	return failed, skipped
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
// stop them all. An empty render or ensure: absent removes the file
// (removeFile). A file already holding the render is not rewritten. Every
// write passes the guard; a refused path or a failed render wraps
// placement.ErrRefused, and the errors are joined. Apply and Remove call it
// after each change, so the files are current at once, and RegenerateAll
// once per run, so a file deleted or refused on one run is restored on the
// next. A rootless placement renders with no root and no items. A scope: user
// entry of a machine placement is left to the per-user helper
// (RegenerateUserFiles).
func (z *ZipInstaller) regenerate(name string) error {
	spec, ok := z.Table.Placements[name]
	if !ok || !generates(spec) {
		return nil
	}
	root, rcs, err := z.fileInputs(name, spec)
	if err != nil {
		return err
	}
	return z.writeFiles(name, spec, root, fileItems(rcs), spec.ScopeFor(z.Guard.GOOS))
}

// writeFiles renders and writes the files entries of spec, placement name,
// whose effective scope is scope: the service passes the placement's own, so
// a machine placement's scope: user entries are left to the per-user helper,
// which passes user for those (RegenerateUserFiles). items are what the
// templates see under root.
func (z *ZipInstaller) writeFiles(name string, spec placement.Spec, root string, items []render.Item, scope string) error {
	ctx := z.fileContext(name, spec, root, items, scope)

	outputs := make([]fileOutput, 0, len(spec.Files))
	var errs []error
	seen := map[string]bool{}
	for i, f := range spec.Files {
		if spec.FileScope(f, z.Guard.GOOS) != scope {
			continue
		}
		named, err := z.mergeName(name, f, scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("placement %q: %w", name, err))
			continue
		}
		path, leaf, ok, err := z.filePath(name, spec, f, root, scope)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			continue
		}
		if !claim(seen, leaf, path) {
			return fmt.Errorf("%w: placement %q: %s is named by two files entries", placement.ErrRefused, name, leaf)
		}
		if f.Absent() {
			outputs = append(outputs, fileOutput{leaf: leaf})
			continue
		}
		data, err := renderFile(fmt.Sprintf("files[%d]", i), f.Template, ctx, z.Facts, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: placement %q: %w", placement.ErrRefused, name, err))
			continue
		}
		o := fileOutput{path: path, leaf: leaf, data: data}
		if f.Merge == placement.MergeAddIfAbsent {
			o.merge, o.named = true, named
			if o.want, err = jsonmerge.Leaves(data); err != nil {
				errs = append(errs, fmt.Errorf("%w: placement %q: files[%d]: %w", placement.ErrRefused, name, i, err))
				continue
			}
		}
		outputs = append(outputs, o)
	}
	if spec.FilesAtomic && len(errs) > 0 {
		return errors.Join(errs...)
	}
	for _, o := range outputs {
		if err := z.applyFile(o, name, len(items)); err != nil {
			errs = append(errs, fmt.Errorf("placement %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// fileInputs returns placement name's resolved root and its receipts, what
// its files render from. A rootless placement has neither.
func (z *ZipInstaller) fileInputs(name string, spec placement.Spec) (string, []receipt.Receipt, error) {
	if spec.Rootless() {
		return "", nil, nil
	}
	root, err := z.resolve(name, spec)
	if err != nil {
		return "", nil, err
	}
	all, err := receipt.List(z.ReceiptDir)
	if err != nil {
		return "", nil, fmt.Errorf("placement %q: unable to read receipts: %w", name, err)
	}
	return root, slices.DeleteFunc(all, func(rc receipt.Receipt) bool { return rc.Placement != name }), nil
}

// claim records the paths a files entry names in seen, its leaf and,
// through a symlink, maybe another path, and reports false when an earlier
// entry named one: two entries writing one file would overwrite each other
// every run.
func claim(seen map[string]bool, leaf, path string) bool {
	keys := []string{strings.ToLower(leaf), strings.ToLower(path)}
	for _, key := range keys {
		if seen[key] {
			return false
		}
	}
	for _, key := range keys {
		seen[key] = key != ""
	}
	return true
}

// fileOutput is one rendered files entry: data for path, or, when data is
// nil, the removal of leaf. A merge entry adds the keys in want to the JSON
// file at path instead (applyMerge); named is that file's path before any
// symlink was resolved.
type fileOutput struct {
	path, leaf string
	named      string
	data       []byte
	merge      bool
	want       []jsonmerge.Leaf
}

// applyFile writes, merges or removes o's file for placement name, rendered
// from items receipts, logging only what changed on disk.
func (z *ZipInstaller) applyFile(o fileOutput, name string, items int) error {
	if o.merge {
		return z.applyMerge(o, name)
	}
	if o.data == nil {
		removed, err := removeFile(o.leaf)
		if removed {
			slog.Info("Removed placement file", "placement", name, "path", o.leaf)
		}
		return err
	}
	if unchanged(o.path, o.data, 0o644) {
		return nil
	}
	if err := writeFileAtomic(o.path, o.data, 0o644); err != nil {
		return err
	}
	slog.Info("Wrote placement file", "placement", name, "path", o.path, "items", items)
	return nil
}

// filePath resolves and checks files entry f of placement name on this OS,
// in scope (its effective scope, the one the guard checks it against),
// under root for a relative path, else at its own path here: path is where
// it is written, with every symlink resolved, and leaf where it is removed,
// with only its parent's resolved (placement.Guard.CheckLeaf). An absent
// entry has no path. ok is false when the entry has no path on this OS.
func (z *ZipInstaller) filePath(name string, spec placement.Spec, f placement.File, root, scope string) (path, leaf string, ok bool, err error) {
	if f.Path != "" {
		local := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(local) {
			return "", "", false, fmt.Errorf("%w: placement %q: file %q is not a path inside the placement", placement.ErrRefused, name, f.Path)
		}
		abs := filepath.Join(root, local)
		leaf, err = z.Guard.CheckLeaf(abs, scope)
		if err == nil && !f.Absent() {
			path, err = z.Guard.Check(abs, scope)
		}
	} else {
		raw, has := f.Paths[z.Guard.GOOS]
		if !has || raw == "" {
			return "", "", false, nil
		}
		leaf, err = z.Guard.ResolveLeaf(raw, scope)
		if err == nil && !f.Absent() {
			path, err = z.Guard.Resolve(raw, scope)
		}
	}
	if err != nil {
		return "", "", false, fmt.Errorf("placement %q: %w", name, err)
	}
	// A path that resolves inside the root, through its own spelling or a
	// symlink, is held to the rules for a relative path, so it never lands
	// in an item tree or on sofcat's own entries. A rootless placement has
	// no root.
	for _, p := range []string{leaf, path} {
		if p == "" || root == "" {
			continue
		}
		if rel, in := z.Guard.RelativeTo(p, root); in {
			if err := spec.CheckFile(rel); err != nil {
				return "", "", false, fmt.Errorf("%w: placement %q: %w", placement.ErrRefused, name, err)
			}
		}
	}
	return path, leaf, true, nil
}

// removeFile removes the regular file at path. It never follows a final
// symlink and never removes a directory: anything at path but a regular
// file is left alone and refused. A missing file is success. removed
// reports whether a file was removed.
func removeFile(path string) (removed bool, err error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case !info.Mode().IsRegular():
		return false, fmt.Errorf("%w: %s is not a regular file; leaving it alone", placement.ErrRefused, path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return true, nil
}

// unchanged reports whether path is a regular file that holds data with
// mode, so a write would change nothing. It always compares with the file
// on disk, so an edited file is rewritten.
func unchanged(path string, data []byte, mode fs.FileMode) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows keeps only a read-only bit, which writeFileAtomic clears
		// for a writable mode.
		if info.Mode().Perm()&0o200 != mode&0o200 {
			return false
		}
	} else if info.Mode() != mode {
		return false
	}
	current, err := os.ReadFile(path)
	return err == nil && bytes.Equal(current, data)
}

// fileContext is what a placement file template of placement name sees in
// scope: the placement at root and items as its items (zip-items guide, "Templates
// for placement files").
func (z *ZipInstaller) fileContext(name string, spec placement.Spec, root string, items []render.Item, scope string) render.FileContext {
	ctx := render.FileContext{
		OS:        z.Guard.GOOS,
		Scope:     scope,
		Placement: spec.View(name, root),
		Items:     items,
	}
	// As in tree templates: the service, in machine scope, has no user.
	if ctx.Scope == placement.ScopeUser {
		ctx.User = z.User
	}
	return ctx
}

// fileItems describes receipts to a template.
func fileItems(rcs []receipt.Receipt) []render.Item {
	items := make([]render.Item, len(rcs))
	for i, rc := range rcs {
		items[i] = render.Item{Name: rc.Item, Version: rc.Version, Managed: rc.Managed, Path: rc.Path, TreeHash: rc.TreeHash, PlacedHash: rc.PlacedHash}
	}
	return items
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
