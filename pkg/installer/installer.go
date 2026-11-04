package installer

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/status"
)

// ErrBlockingApps is returned by Install when a listed blocking application is
// running: the action was skipped and recorded in the report's DeferredItems
// (spec R6). It is errors.Is-able so the dependency walk can cascade the
// deferral instead of treating it as a failure.
var ErrBlockingApps = errors.New("blocking applications running")

// runningBlockingApps is a seam so tests can stub the process snapshot.
var runningBlockingApps = status.RunningBlockingApps

// ProgressFn receives coarse per-item progress events. States emitted per
// item: `downloading`, `installing`/`removing`, `done`/`failed`. pulledBy is
// Runner.PulledBy at the time; the callback must not keep it. The service
// attaches an operation-scoped callback for self-service mutations.
type ProgressFn func(item catalog.Item, pulledBy []string, state string, percent int, message string)

// Runner is the run-scoped install context (K7: no cross-run package globals)
type Runner struct {
	Report      *report.Report
	Checker     *status.Checker
	Emit        ProgressFn // nil = no progress events
	Cancels     *Cancels   // nil = nothing can be withdrawn
	URLPackages string
	CachePath   string
	CheckOnly   bool
	// Zip places zip items; nil fails every zip item as placement_refused.
	Zip *ZipInstaller

	// PulledBy names the items, outermost first, whose install is pulling in
	// the current one as a dependency or updater; empty for a top-level item.
	PulledBy []string
}

// emit sends a progress event if a sink is attached
func (r *Runner) emit(item catalog.Item, state string, percent int, message string) {
	if r.Emit != nil {
		r.Emit(item, r.PulledBy, state, percent, message)
	}
}

// statusCheckStatus is a seam so tests can stub the status check.
var statusCheckStatus = (*status.Checker).CheckStatus

// canceled logs that the user withdrew the item and returns ErrCanceled. The
// service already ended the operation, so no progress event is sent.
func (r *Runner) canceled(item catalog.Item) error {
	slog.Info("Skipping item: canceled by user", "item", item.DisplayName)
	return ErrCanceled
}

// recordFailure appends the item to the run report's FailedItems and returns the error.
func (r *Runner) recordFailure(item catalog.Item, action string, err error) error {
	r.Report.FailedItems = append(r.Report.FailedItems, report.FailedItem{
		Name:    item.Name,
		Version: item.Version,
		Action:  action,
		Error:   err.Error(),
	})
	return err
}

// blockingGate defers the item when any of its explicit blocking_apps are
// running (spec R6): it records a DeferredItem and returns ErrBlockingApps so
// the caller skips — never kills — the action. An empty list means no check. A
// snapshot failure proceeds (does not block the run), matching Munki, which
// treats a failed process listing as "none running".
func (r *Runner) blockingGate(item catalog.Item, action string) error {
	if len(item.BlockingApps) == 0 {
		return nil
	}
	running, err := runningBlockingApps(item.BlockingApps)
	if err != nil {
		slog.Warn("unable to check blocking apps, proceeding", "item", item.DisplayName, "err", err)
		return nil
	}
	if len(running) == 0 {
		return nil
	}
	r.Report.DeferredItems = append(r.Report.DeferredItems, report.DeferredItem{
		Name:    item.Name,
		Version: item.Version,
		Action:  action,
		Reason:  "blocking application(s) running: " + strings.Join(running, ", "),
	})
	slog.Info("Deferring item: blocking application(s) running", "item", item.DisplayName, "apps", strings.Join(running, ", "))
	return ErrBlockingApps
}

// Install determines if action needs to be taken on a item and then
// calls the appropriate function to install or uninstall
func (r *Runner) Install(item catalog.Item, installerType string) (string, error) { //nolint:gocognit // CEILING: known-complex, split in Workstream B (kernel-repair)
	// A zip item's receipts and trees decide its status, not a check block.
	if item.Installer.Type == "zip" {
		return r.installZip(item, installerType)
	}

	// Check the status and determine if any action is needed for this item
	actionNeeded, err := statusCheckStatus(r.Checker, item, installerType, r.CachePath)
	if err != nil {
		// installerType is the action selector (install/update/uninstall), so
		// it is the honest Action value for the report.
		return "", r.recordFailure(item, installerType, fmt.Errorf("unable to check status: %w", err))
	}

	// If no action is needed, record it so the inventory can report the item
	// as already installed (or already absent) rather than unknown
	if !actionNeeded {
		r.Report.NoActionItems = append(r.Report.NoActionItems, report.NoActionItem{
			Name:    item.Name,
			Version: item.Version,
			Action:  installerType,
		})
		return "Item not needed", nil
	}

	// A withdrawn item is skipped before anything runs for it, pre-scripts included.
	if r.Cancels.withdrawn(item.Name) {
		return "", r.canceled(item)
	}

	// Install or uninstall the item
	switch installerType {
	case "install", "update":
		// Check if checkonly mode is enabled
		if r.CheckOnly {
			r.Report.InstalledItems = append(r.Report.InstalledItems, item)
			slog.Info("[CHECK ONLY] Skipping actions", "item", item.DisplayName)
			// Check only mode doesn't perform any action, return
			return "Check only enabled", nil
		}

		// Defer instead of acting if a blocking app is running (R6)
		if err := r.blockingGate(item, installerType); err != nil {
			return "", err
		}

		// Compile the item's URL
		itemURL := r.URLPackages + item.Installer.Location

		// Run PreInstall_Script if needed
		if item.PreScript != "" {
			slog.Info("Running Pre-Install script", "item", item.DisplayName)
			if err := itemScriptFunc(item.PreScript, "preinstall", r.CachePath); err != nil {
				return "", r.recordFailure(item, "install", fmt.Errorf("pre-install script error: %w", err))
			}
		}

		// Run the installer
		out, err := installItemFunc(r, item, itemURL)
		if err != nil {
			return out, err
		}

		// Run PostInstall_Script if needed
		if item.PostScript != "" {
			slog.Info("Running Post-Install script", "item", item.DisplayName)
			// Deliberate double record: the item genuinely installed
			// (InstalledItems) and the post-script genuinely failed (FailedItems).
			if err := itemScriptFunc(item.PostScript, "postinstall", r.CachePath); err != nil {
				return out, r.recordFailure(item, "install", fmt.Errorf("post-install script error: %w", err))
			}
		}
		return out, nil

	case "uninstall":
		if r.CheckOnly {
			r.Report.InstalledItems = append(r.Report.InstalledItems, item)
			slog.Info("[CHECK ONLY] Skipping actions", "item", item.DisplayName)
			// Check only mode doesn't perform any action, return
			return "Check only enabled", nil
		}

		// Defer instead of acting if a blocking app is running (R6)
		if err := r.blockingGate(item, installerType); err != nil {
			return "", err
		}

		// Compile the item's URL
		itemURL := r.URLPackages + item.Uninstaller.Location

		// Run PreUninstall_Script if needed
		if item.PreUninstallScript != "" {
			slog.Info("Running Pre-Uninstall script", "item", item.DisplayName)
			if err := itemScriptFunc(item.PreUninstallScript, "preuninstall", r.CachePath); err != nil {
				return "", r.recordFailure(item, "uninstall", fmt.Errorf("pre-uninstall script error: %w", err))
			}
		}

		// Run the uninstaller
		out, err := uninstallItemFunc(r, item, itemURL)
		if err != nil {
			return out, err
		}

		// Run PostUninstall_Script if needed
		if item.PostUninstallScript != "" {
			slog.Info("Running Post-Uninstall script", "item", item.DisplayName)
			// Deliberate double record: the item genuinely uninstalled
			// (UninstalledItems) and the post-script genuinely failed (FailedItems).
			if err := itemScriptFunc(item.PostUninstallScript, "postuninstall", r.CachePath); err != nil {
				return out, r.recordFailure(item, "uninstall", fmt.Errorf("post-uninstall script error: %w", err))
			}
		}
		return out, nil

	default:
		// Programmer-error guard: process only ever passes install/update/uninstall,
		// so this arm is deliberately exempt from report recording.
		return "", fmt.Errorf("unsupported item type %q for %s", installerType, item.DisplayName)
	}
}
