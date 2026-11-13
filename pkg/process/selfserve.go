package process

import (
	"log/slog"
	"slices"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/status"
)

// statusCheck is a test seam over the status checker, mirroring installerInstall.
var statusCheck = (*status.Checker).CheckStatus

// ReconcileSelfServe reconciles the self-serve manifest against the admin
// manifests and returns the authorized installs and the pass-through uninstalls
// to feed into the run. It mutates selfServe in place for the once-only default
// state machine and reports whether the file needs saving.
//
// Authorization set is the union of every manifest's optional_installs only. As
// in Munki, a default_installs name only asserts intent into the self-serve
// file (analyze.py:747-748); it must also be offered in optional_installs to
// actually install, since installation flows exclusively through the self-serve
// managed_installs filtered against available optional installs (core.py:264-278).
// A default-only name is recorded but filtered out of the returned installs.
func ReconcileSelfServe(selfServe *manifest.Item, manifests []manifest.Item) (installs, uninstalls []string, changed bool) {
	// 1. Defaults (R4): assert each server default exactly once. A name already
	// in the self-serve default_installs record is left alone, so a default the
	// user later removed never re-asserts.
	recorded := make(map[string]bool, len(selfServe.DefaultInstalls))
	for _, name := range selfServe.DefaultInstalls {
		recorded[name] = true
	}
	for _, m := range manifests {
		for _, name := range m.DefaultInstalls {
			if name == "" || recorded[name] {
				continue
			}
			recorded[name] = true
			selfServe.DefaultInstalls = append(selfServe.DefaultInstalls, name)
			if !slices.Contains(selfServe.Installs, name) {
				selfServe.Installs = append(selfServe.Installs, name)
			}
			changed = true
		}
	}
	if changed {
		slices.Sort(selfServe.Installs)
		slices.Sort(selfServe.DefaultInstalls)
	}

	// 2. Authorization (R2): only names the admin offers are processed as
	// installs; anything else is warned and left in the file (it may become
	// available again — we keep user intent).
	available := make(map[string]bool)
	for _, m := range manifests {
		for _, name := range m.OptionalInstalls {
			available[name] = true
		}
	}
	for _, name := range selfServe.Installs {
		if available[name] {
			installs = append(installs, name)
			continue
		}
		slog.Warn("self-serve item not in optional_installs, skipping", "item", name)
	}

	// 3. Uninstalls pass through (R2): a deselected item must be removable even
	// after it leaves the optional list. An item an admin manifest requires
	// (managed_installs) is never removed on a self-service request; the
	// service refuses such a request, and this catches one written to the file
	// some other way. It is left in the file, like an unauthorized install.
	required := make(map[string]bool)
	for _, m := range manifests {
		for _, name := range m.Installs {
			required[name] = true
		}
	}
	for _, name := range selfServe.Uninstalls {
		if required[name] {
			slog.Warn("self-serve uninstall of an item in managed_installs, skipping", "item", name)
			continue
		}
		uninstalls = append(uninstalls, name)
	}
	return installs, uninstalls, changed
}

// GoneUninstalls returns the managed_uninstalls names whose software is
// confirmed gone. An entry that no longer resolves to a catalog item is not
// (the catalog may return), nor is a still-installed item, retried next run,
// nor one whose status cannot be checked. It runs status checks, so callers
// run it on a snapshot outside the self-serve lock.
func GoneUninstalls(uninstalls []string, catalogsMap map[int]map[string]catalog.Item, checker *status.Checker, cachePath string) []string {
	var gone []string
	for _, name := range uninstalls {
		item, ok := removalItem(name, catalogsMap)
		if !ok {
			// removalItem already warned; keep the entry so it can be retried.
			continue
		}
		installed, err := statusCheck(checker, item, "uninstall", cachePath)
		if err != nil {
			slog.Warn("unable to check uninstall status, keeping entry", "item", name, "err", err)
			continue
		}
		if installed {
			// Still on disk (removal failed or deferred); retry next run.
			continue
		}
		gone = append(gone, name)
	}
	return gone
}

// PruneSelfServeUninstalls drops the gone names (from GoneUninstalls) that are
// still in managed_uninstalls, so the user can reinstall later (R5). A name
// the user reselected since the check is no longer there and is left alone. A
// dropped entry's removal is complete, so its selected_by entry goes too: the
// next user to select it becomes its selector (R32a). Returns whether the file
// needs saving.
func PruneSelfServeUninstalls(selfServe *manifest.Item, gone []string) (changed bool) {
	kept := make([]string, 0, len(selfServe.Uninstalls))
	for _, name := range selfServe.Uninstalls {
		if !slices.Contains(gone, name) {
			kept = append(kept, name)
			continue
		}
		delete(selfServe.SelectedBy, name)
		changed = true
	}
	if changed {
		selfServe.Uninstalls = kept
	}
	return changed
}

// Installed reports whether sofcat finds the named catalog item installed on
// this machine, as a depends_check sofcat entry asks. An item no catalog
// offers this machine, or whose status cannot be checked, is not.
func Installed(itemName string, catalogsMap map[int]map[string]catalog.Item, checker *status.Checker, cachePath string) bool {
	item, err := ResolveRemoval(itemName, catalogsMap)
	if err != nil {
		return false
	}
	installed, err := statusCheck(checker, item, "uninstall", cachePath)
	if err != nil {
		slog.Warn("unable to check depends_check item install status", "item", itemName, "err", err)
		return false
	}
	return installed
}
