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
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/identity"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// ZipInstaller places the trees of zip items (docs/zip-items.md). It holds no
// run state, so it works outside a managed run: the service builds one with
// NewMachineZip, and a per-user helper can build one with user roots and its
// own receipt directory.
type ZipInstaller struct {
	// Guard holds the OS, the write roots and the refusals every path passes.
	Guard     placement.Guard
	Harnesses harness.Table
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

	now func() time.Time
}

// NewMachineZip returns the service's zip installer for cfg and the
// repository's harness table: machine roots, receipts in the data directory.
func NewMachineZip(cfg config.Configuration, table harness.Table) *ZipInstaller {
	return &ZipInstaller{
		Guard:       placement.MachineGuard(cfg.AppDataPath),
		Harnesses:   table,
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
	// OutcomeUnchanged (inspect): the tree equals its receipt but not the
	// catalog; apply replaces it with the catalog version.
	OutcomeUnchanged Outcome = "unchanged"
	// OutcomeCurrent: the tree equals the catalog and its receipt is up to date.
	OutcomeCurrent Outcome = "current"
	// OutcomeAdopted: the tree equals the catalog; its receipt is (inspect: is
	// to be) written for the current version.
	OutcomeAdopted Outcome = "adopted"
	// OutcomeInstalled (apply): the catalog tree was placed.
	OutcomeInstalled Outcome = "installed"
	// OutcomeRemoved (remove): the tree and its receipt are gone.
	OutcomeRemoved Outcome = "removed"
	// OutcomeConflict: the tree differs from the catalog and from its receipt,
	// or has none; it is left alone.
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
	// Path is the tree's directory, <placement root>/<item>, when known.
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
	// Spec is the harness table's entry; Known is false when the table lacks
	// the placement, which refuses it.
	Spec  harness.Placement
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

// Targets returns item's placements on this OS, in harness-table order, then
// any the table lacks, by name. Placements whose scope here is user are
// returned apart in user: a machine run skips them, neither failing nor
// succeeding the item.
func (z *ZipInstaller) Targets(item catalog.Item) (machine []Target, user []string) {
	goos := z.Guard.GOOS
	variantFor := func(name string) (catalog.Variant, bool) {
		v, ok := item.Installer.Variants[name][goos]
		return v, ok
	}
	for _, name := range z.Harnesses.Names() {
		v, ok := variantFor(name)
		if !ok {
			continue
		}
		spec := z.Harnesses.Placements[name]
		if spec.ScopeFor(goos) == harness.ScopeUser {
			user = append(user, name)
			continue
		}
		machine = append(machine, Target{Placement: name, Variant: v, Spec: spec, Known: true})
	}
	var unknown []string
	for name := range item.Installer.Variants {
		if _, known := z.Harnesses.Placements[name]; known {
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
		return "", fmt.Errorf("%w: placement %q is not in harnesses.yaml", placement.ErrRefused, t.Placement)
	}
	if t.Spec.Kind != harness.KindDirectory {
		// Marketplace and settings placements have their own writers.
		return "", fmt.Errorf("%w: placement %q is kind %s, which this build does not write", placement.ErrRefused, t.Placement, t.Spec.Kind)
	}
	raw, ok := t.Spec.Path(z.Guard.GOOS)
	if !ok {
		return "", fmt.Errorf("%w: placement %q has no path on %s", placement.ErrRefused, t.Placement, z.Guard.GOOS)
	}
	return z.Guard.Resolve(raw, t.Spec.ScopeFor(z.Guard.GOOS))
}

// checkItemName refuses an item name that cannot be one directory under a
// placement root, or that could collide with sofcat's own dot entries there.
func checkItemName(name string) error {
	if err := checkSegment(name); err != nil || name[0] == '.' || len(name) > 255 {
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
// missing, then catalog-equal (current or adopted), then receipt-equal
// (unchanged), then conflict.
func (z *ZipInstaller) Inspect(item catalog.Item, t Target) Result {
	if err := checkItemName(item.Name); err != nil {
		return fail(t, "", err)
	}
	root, err := z.root(t)
	if err != nil {
		return fail(t, "", err)
	}
	tree := filepath.Join(root, item.Name)
	if err = z.checkExclusive(t, root); err != nil {
		return fail(t, tree, err)
	}
	res := Result{Placement: t.Placement, Path: tree}

	info, err := os.Lstat(tree)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		res.Outcome = OutcomeMissing
		return res
	case err != nil:
		return fail(t, tree, err)
	case !info.IsDir():
		res.Outcome, res.Err = OutcomeConflict, fmt.Errorf("placement %q: %s is not a directory sofcat placed", t.Placement, tree)
		return res
	}
	if empty, _ := dirEmpty(tree); empty {
		res.Outcome = OutcomeMissing
		return res
	}

	rc, hasReceipt, err := receipt.Read(z.ReceiptDir, t.Placement, item.Name)
	if err != nil {
		return fail(t, tree, err)
	}
	// A tree that cannot be hashed (a symlink, a special file) is a user edit.
	hash, hashErr := artifact.TreeHash(tree, z.Guard.GOOS)
	switch {
	case hashErr == nil && hash == t.Variant.TreeHash:
		res.Outcome = OutcomeCurrent
		if !hasReceipt || rc.Version != item.Version || rc.TreeHash != hash || rc.Path != tree || z.managedChanged(rc) {
			res.Outcome = OutcomeAdopted
		}
	case hashErr == nil && hasReceipt && hash == rc.TreeHash:
		res.Outcome = OutcomeUnchanged
	default:
		res.Outcome = OutcomeConflict
		res.Err = fmt.Errorf("placement %q: %s was changed outside sofcat; leaving it alone", t.Placement, tree)
	}
	return res
}

// managedChanged reports whether rc's managed flag is stale, as when a run
// moves the item between optional and managed installs. An installer without
// a Managed func (the service's list call) cannot tell and reports false.
func (z *ZipInstaller) managedChanged(rc receipt.Receipt) bool {
	return z.Managed != nil && rc.Managed != z.Managed(rc.Item)
}

// Fetch downloads t's archive into the cache unless a verified copy is there,
// and returns its path. Cancelling ctx aborts the download.
func (z *ZipInstaller) Fetch(ctx context.Context, t Target) (string, error) {
	// Rooting the location keeps a ".." in a catalog inside the cache.
	rel := path.Clean("/" + t.Variant.Location)
	archive := filepath.Join(z.CachePath, filepath.FromSlash(rel))
	if !download.IfNeeded(ctx, archive, z.URLPackages+t.Variant.Location, t.Variant.Hash) {
		return "", fmt.Errorf("unable to download a valid archive for placement %q", t.Placement)
	}
	return archive, nil
}

// Apply brings item's tree at t to the catalog version from archive, a
// verified copy of t's variant (see Fetch), following Inspect's decision.
// archive is read only when a tree is written.
func (z *ZipInstaller) Apply(item catalog.Item, t Target, archive string) Result {
	res := z.Inspect(item, t)
	switch res.Outcome {
	case OutcomeMissing, OutcomeUnchanged:
	case OutcomeAdopted:
		if err := z.writeReceipt(item, t, res.Path); err != nil {
			return fail(t, res.Path, err)
		}
		slog.Info("Adopted placed tree", "item", item.Name, "placement", t.Placement, "path", res.Path)
		return res
	default:
		return res
	}

	root := filepath.Dir(res.Path)
	if err := z.prepareRoot(t, root); err != nil {
		return fail(t, res.Path, err)
	}
	stage := filepath.Join(root, "."+z.slug()+"-staging-"+item.Name)
	if err := os.RemoveAll(stage); err != nil {
		return fail(t, res.Path, err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := extractArchive(archive, t.Variant.Hash, t.Variant.TreeHash, stage, z.Guard.GOOS); err != nil {
		return fail(t, res.Path, fmt.Errorf("placement %q: %w", t.Placement, err))
	}
	if err := z.swap(item.Name, stage, res.Path, res.Outcome == OutcomeUnchanged); err != nil {
		return fail(t, res.Path, err)
	}
	if err := z.writeReceipt(item, t, res.Path); err != nil {
		return fail(t, res.Path, err)
	}
	slog.Info("Placed tree", "item", item.Name, "version", item.Version, "placement", t.Placement, "path", res.Path)
	res.Outcome = OutcomeInstalled
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

func (z *ZipInstaller) writeReceipt(item catalog.Item, t Target, tree string) error {
	return receipt.Write(z.ReceiptDir, receipt.Receipt{
		Item:           item.Name,
		Version:        item.Version,
		Placement:      t.Placement,
		Path:           tree,
		TreeHash:       t.Variant.TreeHash,
		Managed:        z.Managed != nil && z.Managed(item.Name),
		InstalledAtUTC: z.clock(),
	})
}

// Remove removes the tree rc records, under R15: only when the tree still
// matches the receipt. A tree already gone just drops the receipt.
func (z *ZipInstaller) Remove(rc receipt.Receipt) Result {
	t := Target{Placement: rc.Placement}
	res := Result{Placement: rc.Placement, Path: rc.Path}
	scope := harness.ScopeMachine
	if spec, ok := z.Harnesses.Placements[rc.Placement]; ok {
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
		if hashErr != nil || hash != rc.TreeHash {
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
	return res
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
