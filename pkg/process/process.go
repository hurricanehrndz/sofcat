package process

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// ErrNotInstallable means every valid occurrence of an item has an
// installable_condition this machine fails.
var ErrNotInstallable = errors.New("not installable on this machine")

// errNotResolved means no catalog has a valid occurrence of an item.
var errNotResolved = errors.New("not found or invalid in any catalog")

// firstItem returns the first valid occurrence of an item that may be
// installed on this machine. It logs why there is none and returns false.
func firstItem(itemName string, catalogsMap map[int]map[string]catalog.Item) (catalog.Item, bool) {
	item, err := resolveLogged(itemName, catalogsMap, false)
	return item, err == nil
}

// removalItem is firstItem for removing an item or checking whether it is on
// disk. When no occurrence may be installed here, the first valid one still
// serves, so an item stays removable after its installable_condition stops
// holding.
func removalItem(itemName string, catalogsMap map[int]map[string]catalog.Item) (catalog.Item, bool) {
	item, err := resolveLogged(itemName, catalogsMap, true)
	return item, err == nil
}

// resolveLogged resolves itemName and logs why it could not.
func resolveLogged(itemName string, catalogsMap map[int]map[string]catalog.Item, removal bool) (catalog.Item, error) {
	item, invalidReasons, err := resolveItem(itemName, catalogsMap, removal)
	switch {
	case err == nil:
		return item, nil
	case errors.Is(err, ErrNotInstallable):
		slog.Warn("skipping item: not installable on this machine", "item", itemName, "reason", err)
	case len(invalidReasons) > 0:
		slog.Warn(
			"skipping catalog item: missing required installer/uninstaller type/location fields",
			"item", itemName,
			"reasons", strings.Join(invalidReasons, "; "),
		)
	default:
		slog.Warn("skipping item: not found in any catalog", "item", itemName)
	}
	return catalog.Item{}, err
}

// ResolveItem returns the catalog item the run installs for itemName (the
// first valid occurrence this machine may install, as firstItem picks it)
// without logging. Callers that only report on items use it so they agree
// with the run. The error wraps ErrNotInstallable when only an
// installable_condition stands in the way.
func ResolveItem(itemName string, catalogsMap map[int]map[string]catalog.Item) (catalog.Item, error) {
	item, _, err := resolveItem(itemName, catalogsMap, false)
	return item, err
}

// ResolveRemoval is ResolveItem for an item the run removes, as removalItem
// picks it.
func ResolveRemoval(itemName string, catalogsMap map[int]map[string]catalog.Item) (catalog.Item, error) {
	item, _, err := resolveItem(itemName, catalogsMap, true)
	return item, err
}

// resolveItem finds the first valid occurrence of itemName this machine may
// install and, when there is none, the reasons each occurrence was invalid.
// With removal set, an occurrence the installable_condition rejects serves
// when there is nothing better.
func resolveItem(itemName string, catalogsMap map[int]map[string]catalog.Item, removal bool) (catalog.Item, []string, error) {
	// Get the keys in the map and sort them so we can loop over them in order
	keys := make([]int, 0)
	for k := range catalogsMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	var invalidReasons []string
	var notInstallable *catalog.Item

	// loop through each catalog and return if we find a match
	for _, k := range keys {
		// If
		if item, exists := catalogsMap[k][itemName]; exists {
			missing := missingFields(item)
			if missing == nil {
				// A later catalog may hold a variant this machine can install.
				if item.NotInstallable != "" {
					if notInstallable == nil {
						notInstallable = &item
					}
					continue
				}
				return item, nil, nil
			}

			invalidReasons = append(invalidReasons, fmt.Sprintf("catalog index %d missing required fields: %s", k, strings.Join(missing, ", ")))
		}
	}
	if notInstallable != nil {
		if removal {
			return *notInstallable, nil, nil
		}
		return catalog.Item{}, nil, fmt.Errorf("%w: %s", ErrNotInstallable, notInstallable.NotInstallable)
	}
	return catalog.Item{}, invalidReasons, errNotResolved
}

// missingFields lists the installer and uninstaller fields item lacks, or nil
// when it can be installed or uninstalled. An msix item needs no location: it
// is removed by package name.
func missingFields(item catalog.Item) []string {
	validInstallItem := (item.Installer.Type != "" && item.Installer.Location != "")
	validUninstallItem := (item.Uninstaller.Type != "" && item.Uninstaller.Location != "") ||
		item.Uninstaller.Type == "msix" ||
		item.Installer.Type == "msix"
	if validInstallItem || validUninstallItem {
		return nil
	}

	missing := []string{}
	if item.Installer.Type == "" {
		missing = append(missing, "installer.type")
	}
	if item.Installer.Location == "" {
		missing = append(missing, "installer.location")
	}
	if item.Uninstaller.Type == "" {
		missing = append(missing, "uninstaller.type")
	}
	if item.Uninstaller.Location == "" {
		missing = append(missing, "uninstaller.location")
	}
	return missing
}

// Manifests iterates though the first manifest and any included manifests.
// It drops names with no valid catalog item. Whether this machine may install
// an item is decided where the run acts on it, so the inventory can say why
// one was skipped.
func Manifests(manifests []manifest.Item, catalogsMap map[int]map[string]catalog.Item) (installs, uninstalls, updates []string) {
	// Compile all of the installs, uninstalls, and updates into arrays
	for _, manifestItem := range manifests {
		// Installs
		for _, item := range manifestItem.Installs {
			// Check for the first valid item from our catalogs
			// Continue to the next item in the loop if we get an error
			if _, ok := removalItem(item, catalogsMap); !ok {
				continue
			}

			// If we didnt error, append the item to our installs list
			installs = append(installs, item)
		}
		// Uninstalls
		for _, item := range manifestItem.Uninstalls {
			// Check for the first valid item from our catalogs
			// Continue to the next item in the loop if we get an error
			if _, ok := removalItem(item, catalogsMap); !ok {
				continue
			}

			// If we didnt error, append the item to our uninstalls list
			uninstalls = append(uninstalls, item)
		}
		// Updates
		for _, item := range manifestItem.Updates {
			// Check for the first valid item from our catalogs
			// Continue to the next item in the loop if we get an error
			if _, ok := removalItem(item, catalogsMap); !ok {
				continue
			}

			// If we didnt error, append the item to our updates list
			updates = append(updates, item)
		}
	}
	return
}

// This abstraction allows us to override when testing
var installerInstall = (*installer.Runner).Install

// installOne runs a single item action and logs failures; the run continues
// past item failures (Munki semantics) — the installer already recorded the
// failure in the report.
func installOne(r *installer.Runner, item catalog.Item, installerType string) {
	if _, err := installerInstall(r, item, installerType); err != nil {
		if errors.Is(err, installer.ErrBlockingApps) {
			// The installer already recorded the deferral; not a failure (R6).
			slog.Info("item deferred: blocking application(s) running", "item", item.DisplayName)
			return
		}
		slog.Warn("item action failed", "item", item.DisplayName, "err", err)
	}
}

// depState tracks an item's progress in the per-run dependency walk (K4)
type depState int

const (
	depInProgress depState = iota + 1
	depSucceeded
	depFailed
	depDeferred
	// depNotInstallable: the item's installable_condition rejects this
	// machine. It is skipped, not failed; an item that depends on it fails.
	depNotInstallable
)

// recordFailedItem records a gating failure (cycle, missing dep, or skipped
// dependent) that the installer never saw and so never recorded itself.
// version may be empty when the item was never resolved from a catalog.
func recordFailedItem(r *installer.Runner, name, version string, err error) {
	r.Report.FailedItems = append(r.Report.FailedItems, report.FailedItem{
		Name:    name,
		Version: version,
		Action:  "install",
		Error:   err.Error(),
	})
}

// recordDeferredItem records a cascade deferral: a dependent skipped because
// one of its dependencies was deferred (R6/R12). The installer records its own
// direct deferrals; this covers only the walk's cascade.
func recordDeferredItem(r *installer.Runner, name, version, reason string) {
	r.Report.DeferredItems = append(r.Report.DeferredItems, report.DeferredItem{
		Name:    name,
		Version: version,
		Action:  "install",
		Reason:  reason,
	})
}

// pullIn runs walk with name pushed on r.PulledBy, so the progress of every
// item walk installs says name's install pulled it in.
func pullIn(r *installer.Runner, name string, walk func() bool) bool {
	r.PulledBy = append(r.PulledBy, name)
	defer func() { r.PulledBy = r.PulledBy[:len(r.PulledBy)-1] }()
	return walk()
}

// installWithDeps installs itemName's dependencies depth-first and then the
// item itself (K4, spec R6). visited gates each item to one attempt per run
// and detects cycles; a failed, invalid, or missing dependency skips its
// dependents. Returns true if the item installed (or was already up to date).
func installWithDeps(itemName string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner, visited map[string]depState, index map[string][]string) bool {
	switch visited[itemName] {
	case depSucceeded:
		return true
	case depFailed, depDeferred, depNotInstallable:
		return false
	case depInProgress:
		// Cycle: the item's own frame is still on the stack, so this lookup
		// already succeeded there and silently returns the same item.
		item, _ := firstItem(itemName, catalogsMap)
		slog.Warn("dependency cycle detected, skipping item", "item", item.DisplayName)
		recordFailedItem(r, item.Name, item.Version, fmt.Errorf("dependency cycle detected"))
		visited[itemName] = depFailed
		return false
	}

	// resolveLogged logs why the item is missing, invalid, or not installable
	item, err := resolveLogged(itemName, catalogsMap, false)
	if errors.Is(err, ErrNotInstallable) {
		visited[itemName] = depNotInstallable
		return false
	}
	if err != nil {
		recordFailedItem(r, itemName, "", err)
		visited[itemName] = depFailed
		return false
	}

	visited[itemName] = depInProgress
	for _, dependency := range item.Dependencies {
		if !pullIn(r, itemName, func() bool { return installWithDeps(dependency, catalogsMap, r, visited, index) }) {
			if visited[itemName] == depFailed {
				// a cycle back to this item already recorded it
				return false
			}
			// A deferred dependency cascades: the dependent is deferred, not
			// failed, and retried next run (R6).
			if visited[dependency] == depDeferred {
				slog.Info("deferring item: dependency deferred", "item", item.DisplayName, "dependency", dependency)
				recordDeferredItem(r, item.Name, item.Version, fmt.Sprintf("dependency %s deferred", dependency))
				visited[itemName] = depDeferred
				return false
			}
			if visited[dependency] == depNotInstallable {
				slog.Warn("skipping item: dependency not installable on this machine", "item", item.DisplayName, "dependency", dependency)
				recordFailedItem(r, item.Name, item.Version, fmt.Errorf("dependency %s is not installable on this machine", dependency))
				visited[itemName] = depFailed
				return false
			}
			slog.Warn("skipping item: dependency failed", "item", item.DisplayName, "dependency", dependency)
			recordFailedItem(r, item.Name, item.Version, fmt.Errorf("dependency %s failed", dependency))
			visited[itemName] = depFailed
			return false
		}
	}

	// Install the item; the installer records its own failures in the report
	if _, err := installerInstall(r, item, "install"); err != nil {
		if errors.Is(err, installer.ErrBlockingApps) {
			// The installer already recorded the deferral (R6); mark deferred
			// so dependents cascade rather than fail.
			visited[itemName] = depDeferred
			return false
		}
		slog.Warn("item action failed", "item", item.DisplayName, "err", err)
		visited[itemName] = depFailed
		return false
	}
	visited[itemName] = depSucceeded

	// update_for (R7): now that this referent installed, its updaters are
	// processed as installs through the same walk. visited keeps this cycle-safe
	// and once-per-run; a deferred or failed referent never reaches here, so its
	// updaters are skipped (Munki: dependents of skipped work are skipped).
	for _, updater := range index[itemName] {
		pullIn(r, itemName, func() bool { return installWithDeps(updater, catalogsMap, r, visited, index) })
	}
	return true
}

// Installs prepares and then installs an array of items with their
// dependencies resolved recursively (K4, spec R6). index maps referent names to
// their update_for updaters so updaters ride along after their referent (R7).
func Installs(installs []string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner, index map[string][]string) {
	visited := make(map[string]depState)
	for _, item := range installs {
		installWithDeps(item, catalogsMap, r, visited, index)
	}
}

// Uninstalls prepares and then installs an array of items
func Uninstalls(uninstalls []string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner) {
	// Iterate through the uninstalls array and uninstall the item
	for _, item := range uninstalls {
		// Get the first valid item from our catalogs
		// Continue to the next item in the loop if we get an error
		validItem, ok := removalItem(item, catalogsMap)
		if !ok {
			continue
		}
		// Uninstall the item
		installOne(r, validItem, "uninstall")
	}
}

// Updates prepares and then installs an array of items
func Updates(updates []string, catalogsMap map[int]map[string]catalog.Item, r *installer.Runner) {
	// Iterate through the updates array and update the item **if it is already installed**
	for _, item := range updates {
		// Get the first valid item from our catalogs
		// Continue to the next item in the loop if we get an error
		validItem, ok := firstItem(item, catalogsMap)
		if !ok {
			continue
		}
		// Update the item
		installOne(r, validItem, "update")
	}
}

// dirEmpty returns true if the directory is empty
func dirEmpty(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Try to get the first item in the directory
	_, err = f.Readdir(1)

	// If the we recevie an EOF error, the dir is empty
	return errors.Is(err, io.EOF)
}

// fileOld returns true if the file is older than
// the limit defined in the variable `days`
func fileOld(info os.FileInfo) bool {
	// Age of the file
	fileAge := time.Since(info.ModTime())

	// Our limit
	days := 5

	// Convert from days
	hours := days * 24
	ageLimit := time.Duration(hours) * time.Hour

	// If the file is older than our limit, return true
	return fileAge > ageLimit
}

// This abstraction allows us to override when testing
var osRemove = os.Remove

// CleanUp checks the age of items in the cache and removes if older than 10 days
func CleanUp(cachePath string) {
	// Clean up old files
	err := filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("Failed to access path", "path", path, "err", err)
			return err
		}
		// If not a directory and older that our limit, delete
		if !info.IsDir() && fileOld(info) {
			slog.Info("Cleaning old cached file", "path", path)
			if err := osRemove(path); err != nil {
				slog.Warn("Failed to remove path", "path", path, "err", err)
			}
			return nil
		}
		return nil
	})
	if err != nil {
		slog.Warn("error walking path", "path", cachePath, "err", err)
		return
	}

	// Clean up empty directories
	err = filepath.Walk(cachePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("Failed to access path", "path", path, "err", err)
			return err
		}

		// If a dir and empty, delete
		if info.IsDir() && dirEmpty(path) {
			slog.Info("Cleaning empty directory", "path", path)
			if err := osRemove(path); err != nil {
				slog.Warn("Failed to remove path", "path", path, "err", err)
			}
			return nil

		}
		return nil
	})
	if err != nil {
		slog.Warn("error walking path", "path", cachePath, "err", err)
		return
	}
}
