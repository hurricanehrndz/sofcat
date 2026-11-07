package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// ZipInstaller places the trees of zip items (site/src/content/docs/guides/zip-items.md). It holds no
// run state, so it works outside a managed run: the service builds one with
// NewMachineZip, and a per-user helper can build one with user roots and its
// own receipt directory.
type ZipInstaller struct {
	// Guard holds the OS, the write roots and the refusals every path passes.
	Guard placement.Guard
	Table placement.Table
	// ReceiptDir holds this installer's receipts.
	ReceiptDir string
	// Slug names the ownership marker and staging directories; identity.Slug
	// when empty.
	Slug string
	// CachePath and URLPackages locate archives: a variant's location is
	// downloaded from URLPackages+location to CachePath/location.
	CachePath   string
	URLPackages string
	// Managed reports whether a manifest lists item in managed_installs, for
	// the receipt's managed flag. Nil means never.
	Managed func(item string) bool
	// Held reports whether a held conditional block lists item. A held
	// item's managed flag is never rewritten: the run cannot tell whether it
	// is managed. Nil means nothing is held.
	Held func(item string) bool
	// Facts are the run's facts (pkg/facts), which placement file and tree
	// templates read through fact and cond. Nil means the machine has no
	// facts, so a template that reads one fails.
	Facts conditions.Facts
	// User is the login name, without domain, of the account this installer
	// runs as: the tree templates' .User in user scope. The service leaves
	// it empty; the per-user helper sets it.
	User string

	// renderer decides what sofcat renders inside a tree; the tree's own
	// templates (treeTemplates) when nil.
	renderer treeRenderer
	now      func() time.Time
}

func (z *ZipInstaller) templates() treeRenderer {
	if z.renderer != nil {
		return z.renderer
	}
	return treeTemplates{z}
}

// NewMachineZip returns the service's zip installer for cfg and the
// repository's placement table: machine roots, receipts in the data directory.
func NewMachineZip(cfg config.Configuration, table placement.Table) *ZipInstaller {
	return &ZipInstaller{
		Guard:       placement.MachineGuard(cfg.AppDataPath),
		Table:       table,
		ReceiptDir:  receipt.MachineDir(cfg.AppDataPath),
		CachePath:   cfg.CachePath,
		URLPackages: cfg.URLPackages,
	}
}

// Outcome is what an inspect, apply or remove step found or did for one
// placement.
type Outcome string

const (
	// OutcomeMissing (inspect): no tree yet; apply installs it.
	OutcomeMissing Outcome = "missing"
	// OutcomeUnchanged (inspect): the tree equals its receipt's placed_hash
	// but the receipt's tree_hash is not the catalog's; apply replaces it
	// with the catalog version.
	OutcomeUnchanged Outcome = "unchanged"
	// OutcomeCurrent: the tree equals its receipt's placed_hash, the receipt
	// matches the catalog and its rendered inputs are what this run would
	// render with.
	OutcomeCurrent Outcome = "current"
	// OutcomeAdopted: the tree equals its receipt's placed_hash with a stale
	// receipt or stale inputs, or equals the catalog's tree_hash as shipped;
	// its receipt is (inspect: is to be) written for the current version,
	// after a render in place where inputs or templates call for one.
	OutcomeAdopted Outcome = "adopted"
	// OutcomeInstalled (apply): the catalog tree was placed.
	OutcomeInstalled Outcome = "installed"
	// OutcomeRemoved (remove): the tree and its receipt are gone.
	OutcomeRemoved Outcome = "removed"
	// OutcomeConflict: the tree matches neither its receipt's placed_hash nor
	// the catalog's tree_hash, or has no receipt and differs from the
	// catalog; it is left alone.
	OutcomeConflict Outcome = "conflict"
	// OutcomeRefused: the placement may not be written.
	OutcomeRefused Outcome = "refused"
	// OutcomeFailed: an error other than a conflict or a refusal.
	OutcomeFailed Outcome = "failed"
)

// Result is one placement's outcome. Err is set for OutcomeConflict,
// OutcomeRefused and OutcomeFailed.
type Result struct {
	Placement string
	// Path is the tree's directory, <placement root>/<item> (with
	// items_dir, <placement root>/<items_dir>/<item>), when known.
	Path    string
	Outcome Outcome
	Err     error
}

// Code is the report error code of a failed result: placement_conflict,
// placement_refused, or "" for the generic item_failed.
func (r Result) Code() string {
	switch r.Outcome {
	case OutcomeConflict:
		return report.CodePlacementConflict
	case OutcomeRefused:
		return report.CodePlacementRefused
	default:
		return ""
	}
}

// Failed reports whether the result fails the item.
func (r Result) Failed() bool { return r.Err != nil }

// Target is one placement a zip item writes on this OS.
type Target struct {
	Placement string
	Variant   catalog.Variant
	// Spec is the placement table's entry; Known is false when the table lacks
	// the placement, which refuses it.
	Spec  placement.Spec
	Known bool
}

func (z *ZipInstaller) slug() string {
	if z.Slug != "" {
		return z.Slug
	}
	return identity.Slug
}

func (z *ZipInstaller) clock() time.Time {
	if z.now != nil {
		return z.now().UTC()
	}
	return time.Now().UTC()
}

// Targets returns item's placements on this OS, in table order, then
// any the table lacks, by name. Placements whose scope here is user are
// returned apart in user: a machine run skips them, neither failing nor
// succeeding the item.
func (z *ZipInstaller) Targets(item catalog.Item) (machine []Target, user []string) {
	goos := z.Guard.GOOS
	variantFor := func(name string) (catalog.Variant, bool) {
		v, ok := item.Installer.Variants[name][goos]
		return v, ok
	}
	for _, name := range z.Table.Names() {
		v, ok := variantFor(name)
		if !ok {
			continue
		}
		spec := z.Table.Placements[name]
		if spec.ScopeFor(goos) == placement.ScopeUser {
			user = append(user, name)
			continue
		}
		machine = append(machine, Target{Placement: name, Variant: v, Spec: spec, Known: true})
	}
	var unknown []string
	for name := range item.Installer.Variants {
		if _, known := z.Table.Placements[name]; known {
			continue
		}
		if _, ok := variantFor(name); ok {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	for _, name := range unknown {
		v, _ := variantFor(name)
		machine = append(machine, Target{Placement: name, Variant: v})
	}
	return machine, user
}

// root resolves and checks the target's placement root.
func (z *ZipInstaller) root(t Target) (string, error) {
	if !t.Known {
		return "", fmt.Errorf("%w: placement %q is not in placements.yaml", placement.ErrRefused, t.Placement)
	}
	return z.resolve(t.Placement, t.Spec)
}

// resolve expands, resolves and checks placement name's path on this OS.
func (z *ZipInstaller) resolve(name string, spec placement.Spec) (string, error) {
	raw, ok := spec.Path(z.Guard.GOOS)
	if !ok {
		return "", fmt.Errorf("%w: placement %q has no path on %s", placement.ErrRefused, name, z.Guard.GOOS)
	}
	path, err := z.Guard.Resolve(raw, spec.ScopeFor(z.Guard.GOOS))
	if err != nil {
		return "", fmt.Errorf("placement %q: %w", name, err)
	}
	return path, nil
}

// treeIn is item's tree under its placement root: <root>/<item>, or
// <root>/<items_dir>/<item>.
func treeIn(spec placement.Spec, root, item string) string {
	return filepath.Join(root, filepath.FromSlash(spec.ItemsDir), item)
}

// checkItemName refuses an item name that cannot be one directory under a
// placement root, or that could collide with sofcat's own dot entries there.
func checkItemName(name string) error {
	if err := placement.CheckSegment(name); err != nil || name[0] == '.' || len(name) > 255 {
		return fmt.Errorf("%w: item name %q cannot name a directory", placement.ErrRefused, name)
	}
	return nil
}

func (z *ZipInstaller) marker(root string) string {
	return filepath.Join(root, "."+z.slug()+"-owned")
}

// checkExclusive refuses an exclusive root that exists, is not empty and has
// no ownership marker.
func (z *ZipInstaller) checkExclusive(t Target, root string) error {
	if !t.Spec.Exclusive {
		return nil
	}
	if _, err := os.Lstat(z.marker(root)); err == nil {
		return nil
	}
	empty, err := dirEmpty(root)
	if err != nil || empty {
		return err
	}
	return fmt.Errorf("%w: exclusive placement %q has files sofcat did not put there", placement.ErrRefused, t.Placement)
}

// dirEmpty reports whether dir is missing or empty.
func dirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// fail builds a result from err, telling refusals from other failures.
func fail(t Target, tree string, err error) Result {
	outcome := OutcomeFailed
	if errors.Is(err, placement.ErrRefused) {
		outcome = OutcomeRefused
	}
	return Result{Placement: t.Placement, Path: tree, Outcome: outcome, Err: err}
}

// Inspect decides, without writing, what Apply would do for item at t (R14):
// missing; then a tree equal to its receipt's placed_hash, sofcat's own
// (current, adopted when the receipt or the rendered inputs are stale, or
// unchanged when the catalog moved on); then one equal to the catalog's
// tree_hash that sofcat did not place (adopted); then conflict.
func (z *ZipInstaller) Inspect(item catalog.Item, t Target) Result {
	return z.inspect(item, t).Result
}

// finding is inspectTree's decision with what Apply needs to act on it.
type finding struct {
	Result
	// root is the placement root once resolved.
	root string
	// render is set for an adopted tree whose files must be rendered in
	// place: one sofcat did not place, or whose rendered inputs changed. An
	// adopted tree with current outputs only gets its receipt rewritten.
	render bool
	// shipped is set for an adopted tree that equals the catalog's tree as
	// shipped, so it must pass the shipped checks before it is rendered.
	shipped bool
}

// inspect is Inspect with the placement root once resolved.
func (z *ZipInstaller) inspect(item catalog.Item, t Target) finding {
	if err := checkItemName(item.Name); err != nil {
		return finding{Result: fail(t, "", err)}
	}
	root, err := z.root(t)
	if err != nil {
		return finding{Result: fail(t, "", err)}
	}
	f := z.inspectTree(item, t, root)
	f.root = root
	return f
}

func (z *ZipInstaller) inspectTree(item catalog.Item, t Target, root string) finding {
	tree := treeIn(t.Spec, root, item.Name)
	if err := z.checkExclusive(t, root); err != nil {
		return finding{Result: fail(t, tree, err)}
	}
	f := finding{Result: Result{Placement: t.Placement, Path: tree}}

	info, err := os.Lstat(tree)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.Outcome = OutcomeMissing
		return f
	case err != nil:
		return finding{Result: fail(t, tree, err)}
	case !info.IsDir():
		f.Outcome, f.Err = OutcomeConflict, fmt.Errorf("placement %q: %s is not a directory sofcat placed", t.Placement, tree)
		return f
	}
	if empty, _ := dirEmpty(tree); empty {
		f.Outcome = OutcomeMissing
		return f
	}

	rc, hasReceipt, err := receipt.Read(z.ReceiptDir, t.Placement, item.Name)
	if err != nil {
		return finding{Result: fail(t, tree, err)}
	}
	// A tree that cannot be hashed (a symlink, a special file) is a user edit.
	hash, hashErr := artifact.TreeHash(tree, z.Guard.GOOS)
	switch {
	case hashErr == nil && hasReceipt && hash == rc.PlacedHash:
		// Sofcat's own tree, intact.
		if rc.TreeHash != t.Variant.TreeHash {
			f.Outcome = OutcomeUnchanged
			return f
		}
		match, err := z.templates().inputsMatch(item, t, root, tree, rc, z.managedNow(rc))
		if err != nil {
			return finding{Result: fail(t, tree, err)}
		}
		switch {
		case !match:
			f.Outcome, f.render = OutcomeAdopted, true
		case rc.Version != item.Version || rc.Path != tree || z.managedChanged(rc):
			f.Outcome = OutcomeAdopted
		default:
			f.Outcome = OutcomeCurrent
		}
	case hashErr == nil && hash == t.Variant.TreeHash:
		// The catalog's tree as shipped, which sofcat did not place: put
		// there by hand, or before sofcat rendered anything.
		f.Outcome, f.render, f.shipped = OutcomeAdopted, true, true
	default:
		f.Outcome = OutcomeConflict
		f.Err = fmt.Errorf("placement %q: %s was changed outside sofcat; leaving it alone", t.Placement, tree)
	}
	return f
}

// managedChanged reports whether rc's managed flag is stale, as when a run
// moves the item between optional and managed installs.
func (z *ZipInstaller) managedChanged(rc receipt.Receipt) bool {
	return rc.Managed != z.managedNow(rc)
}

// managedNow is rc's item's managed flag as this run sees it. An installer
// without a Managed func (the service's list call) cannot tell and keeps the
// receipt's, as it does for a held item.
func (z *ZipInstaller) managedNow(rc receipt.Receipt) bool {
	if z.Managed == nil || z.held(rc.Item) {
		return rc.Managed
	}
	return z.Managed(rc.Item)
}

func (z *ZipInstaller) held(item string) bool {
	return z.Held != nil && z.Held(item)
}

// Fetch downloads t's archive into the cache unless a verified copy is there,
// and returns its path. Cancelling ctx aborts the download.
func (z *ZipInstaller) Fetch(ctx context.Context, t Target, progress download.Progress) (string, error) {
	// The location names the cached copy, so a crafted one must not leave
	// the cache: every segment of the cleaned path must be a plain name.
	rel := path.Clean(t.Variant.Location)
	for _, seg := range strings.Split(rel, "/") {
		if err := placement.CheckSegment(seg); err != nil {
			return "", fmt.Errorf("placement %q: archive location %q refused: %w", t.Placement, t.Variant.Location, err)
		}
	}
	archive := filepath.Join(z.CachePath, filepath.FromSlash(rel))
	if !download.IfNeeded(ctx, archive, z.URLPackages+t.Variant.Location, t.Variant.Hash, progress) {
		return "", fmt.Errorf("unable to download a valid archive for placement %q", t.Placement)
	}
	return archive, nil
}

// Apply brings item's tree at t to the catalog version from archive, a
// verified copy of t's variant (see Fetch), following Inspect's decision.
// archive is read only when a tree is written: an adopted tree is rendered
// in place from the templates it holds, without the archive.
//
// After it changes a placement (an install, an upgrade, or a receipt
// adopted or refreshed, as when only the managed flag changed) it
// regenerates the placement's files; a failure there fails the result.
func (z *ZipInstaller) Apply(item catalog.Item, t Target, archive string) Result {
	f := z.inspect(item, t)
	res := f.Result
	switch res.Outcome {
	case OutcomeMissing, OutcomeUnchanged:
	case OutcomeAdopted:
		// A tree as shipped is held to the same rules as a staged one; a
		// placed tree legitimately holds each template beside its output.
		if f.shipped {
			if err := checkShipped(res.Path, z.Guard.GOOS); err != nil {
				return fail(t, res.Path, fmt.Errorf("placement %q: %w", t.Placement, err))
			}
		}
		if err := z.adopt(item, t, f.root, res.Path, f.render); err != nil {
			return fail(t, res.Path, err)
		}
		slog.Info("Adopted placed tree", "item", item.Name, "placement", t.Placement, "path", res.Path, "rendered", f.render)
		return z.regenerated(t, res)
	default:
		return res
	}

	if err := z.prepareRoot(t, f.root); err != nil {
		return fail(t, res.Path, err)
	}
	// The tree's directory: the root, or its items_dir.
	parent := filepath.Dir(res.Path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fail(t, res.Path, err)
	}
	// items_dir may already exist as a link somewhere else; the tree goes
	// only where the guard allows.
	if _, err := z.Guard.Check(parent, t.Spec.ScopeFor(z.Guard.GOOS)); err != nil {
		return fail(t, res.Path, fmt.Errorf("placement %q: %w", t.Placement, err))
	}
	stage := filepath.Join(parent, "."+z.slug()+"-staging-"+item.Name)
	if err := os.RemoveAll(stage); err != nil {
		return fail(t, res.Path, err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := extractArchive(archive, t.Variant.Hash, t.Variant.TreeHash, stage, z.Guard.GOOS); err != nil {
		return fail(t, res.Path, fmt.Errorf("placement %q: %w", t.Placement, err))
	}
	// The archive's hashes are checked; now the tree becomes this device's.
	// Staging is discarded on failure, so its undo is not needed.
	if err := checkShipped(stage, z.Guard.GOOS); err != nil {
		return fail(t, res.Path, fmt.Errorf("placement %q: %w", t.Placement, err))
	}
	placed, recs, _, err := z.renderTree(item, t, f.root, stage)
	if err != nil {
		return fail(t, res.Path, fmt.Errorf("placement %q: %w", t.Placement, err))
	}
	if err := z.swap(item.Name, stage, res.Path, res.Outcome == OutcomeUnchanged); err != nil {
		return fail(t, res.Path, err)
	}
	if err := z.writeReceipt(item, t, res.Path, placed, recs); err != nil {
		return fail(t, res.Path, err)
	}
	slog.Info("Placed tree", "item", item.Name, "version", item.Version, "placement", t.Placement, "path", res.Path, "rendered", len(recs))
	res.Outcome = OutcomeInstalled
	return z.regenerated(t, res)
}

// adopt writes the receipt for a tree Inspect found adopted under root. With
// render it first renders the tree's templates in place, all or nothing, and
// records the hash of the result; otherwise the receipt keeps what it
// recorded of the tree, which is unchanged.
func (z *ZipInstaller) adopt(item catalog.Item, t Target, root, tree string, render bool) error {
	if !render {
		rc, ok, err := receipt.Read(z.ReceiptDir, t.Placement, item.Name)
		if err != nil {
			return err
		}
		if ok {
			return z.writeReceipt(item, t, tree, rc.PlacedHash, rc.Rendered)
		}
	}
	placed, recs, undo, err := z.renderTree(item, t, root, tree)
	if err != nil {
		return fmt.Errorf("placement %q: %w", t.Placement, err)
	}
	// Without the receipt the tree would match neither hash: put the
	// previous bytes back, so the next run retries.
	if err := z.writeReceipt(item, t, tree, placed, recs); err != nil {
		return fmt.Errorf("placement %q: %w", t.Placement, errors.Join(err, undo()))
	}
	return nil
}

// renderTree renders item's templates in dir, its tree under root staged or
// in place, through writeRendered, and returns the tree's hash as written
// with the records for its receipt and an undo that puts the tree's previous
// bytes back. A tree that cannot be rehashed is put back before the error
// returns.
func (z *ZipInstaller) renderTree(item catalog.Item, t Target, root, dir string) (string, []receipt.Rendered, func() error, error) {
	managed, err := z.receiptManaged(item.Name, t.Placement)
	if err != nil {
		return "", nil, nil, err
	}
	outs, err := z.templates().render(item, t, root, dir, managed)
	if err != nil {
		return "", nil, nil, err
	}
	undo, err := writeRendered(dir, outs)
	if err != nil {
		return "", nil, nil, err
	}
	placed, err := artifact.TreeHash(dir, z.Guard.GOOS)
	if err != nil {
		return "", nil, nil, errors.Join(err, undo())
	}
	return placed, records(outs), undo, nil
}

// regenerated regenerates the files of t's placement after res
// changed it, failing res when that fails.
func (z *ZipInstaller) regenerated(t Target, res Result) Result {
	if err := z.regenerate(t.Placement); err != nil {
		return fail(t, res.Path, err)
	}
	return res
}

// prepareRoot creates the placement root if needed. An exclusive root sofcat
// creates, or finds empty, gets the ownership marker.
func (z *ZipInstaller) prepareRoot(t Target, root string) error {
	empty, err := dirEmpty(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if t.Spec.Exclusive && empty {
		return os.WriteFile(z.marker(root), nil, 0o644)
	}
	return nil
}

// swap moves the staged tree to tree. replace moves the old tree aside first
// and deletes it afterwards; Windows cannot rename onto an existing directory.
// A missing tree may still be an empty directory, which is removed.
func (z *ZipInstaller) swap(item, stage, tree string, replace bool) error {
	if !replace {
		if err := os.Remove(tree); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.Rename(stage, tree)
	}
	old := filepath.Join(filepath.Dir(tree), "."+z.slug()+"-old-"+item)
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(tree, old); err != nil {
		return err
	}
	if err := os.Rename(stage, tree); err != nil {
		if restore := os.Rename(old, tree); restore != nil {
			slog.Warn("unable to restore the previous tree", "path", tree, "err", restore)
		}
		return err
	}
	if err := os.RemoveAll(old); err != nil {
		slog.Warn("unable to remove the previous tree", "path", old, "err", err)
	}
	return nil
}

// writeReceipt records item's tree at t: the catalog's tree hash, placed as
// the hash of the tree as written and rendered as its rendered files.
func (z *ZipInstaller) writeReceipt(item catalog.Item, t Target, tree, placed string, rendered []receipt.Rendered) error {
	managed, err := z.receiptManaged(item.Name, t.Placement)
	if err != nil {
		return err
	}
	return receipt.Write(z.ReceiptDir, receipt.Receipt{
		Item:           item.Name,
		Version:        item.Version,
		Placement:      t.Placement,
		Path:           tree,
		TreeHash:       t.Variant.TreeHash,
		PlacedHash:     placed,
		Rendered:       rendered,
		Managed:        managed,
		InstalledAtUTC: z.clock(),
	})
}

// receiptManaged is the managed flag item's receipt in placement records
// now. A held item keeps the flag its existing receipt records.
func (z *ZipInstaller) receiptManaged(item, placement string) (bool, error) {
	if z.held(item) {
		rc, ok, err := receipt.Read(z.ReceiptDir, placement, item)
		if err != nil {
			return false, err
		}
		if ok {
			return rc.Managed, nil
		}
	}
	return z.Managed != nil && z.Managed(item), nil
}

// Remove removes the tree rc records, under R15: only when the tree still
// matches the receipt's placed_hash. A tree already gone just drops the
// receipt. Like Apply, removing a tree regenerates the placement's
// files.
func (z *ZipInstaller) Remove(rc receipt.Receipt) Result {
	t := Target{Placement: rc.Placement}
	res := Result{Placement: rc.Placement, Path: rc.Path}
	scope := placement.ScopeMachine
	if spec, ok := z.Table.Placements[rc.Placement]; ok {
		scope = spec.ScopeFor(z.Guard.GOOS)
	}
	if err := checkItemName(rc.Item); err != nil {
		return fail(t, rc.Path, err)
	}
	if filepath.Base(rc.Path) != rc.Item {
		return fail(t, rc.Path, fmt.Errorf("%w: receipt path %s does not name item %q", placement.ErrRefused, rc.Path, rc.Item))
	}
	root, err := z.Guard.Check(filepath.Dir(rc.Path), scope)
	if err != nil {
		return fail(t, rc.Path, err)
	}
	tree := filepath.Join(root, rc.Item)

	_, err = os.Lstat(tree)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fail(t, rc.Path, err)
	default:
		hash, hashErr := artifact.TreeHash(tree, z.Guard.GOOS)
		if hashErr != nil || hash != rc.PlacedHash {
			res.Outcome = OutcomeConflict
			res.Err = fmt.Errorf("placement %q: %s was changed outside sofcat; leaving it alone", rc.Placement, rc.Path)
			return res
		}
		if err := os.RemoveAll(tree); err != nil {
			return fail(t, rc.Path, err)
		}
	}
	if err := receipt.Delete(z.ReceiptDir, rc.Placement, rc.Item); err != nil {
		return fail(t, rc.Path, err)
	}
	slog.Info("Removed placed tree", "item", rc.Item, "placement", rc.Placement, "path", rc.Path)
	res.Outcome = OutcomeRemoved
	return z.regenerated(t, res)
}

// Installed is a zip item's install status (R15), from receipts and placed
// trees only: true when every machine-scope placement for this OS has a
// receipt and a non-empty tree at the receipt's path, whatever its hash
// (current, older, or edited by the user). An edited tree is still sofcat's
// to remove, so a pending removal that met a conflict stays pending. An item
// with no machine-scope placement, a placement without a receipt, or a
// receipt whose tree is missing or empty is not installed.
func (z *ZipInstaller) Installed(item catalog.Item) bool {
	machine, _ := z.Targets(item)
	if len(machine) == 0 {
		return false
	}
	for _, t := range machine {
		rc, ok, err := receipt.Read(z.ReceiptDir, t.Placement, item.Name)
		if err != nil || !ok {
			return false
		}
		if empty, err := dirEmpty(rc.Path); err != nil || empty {
			return false
		}
	}
	return true
}
