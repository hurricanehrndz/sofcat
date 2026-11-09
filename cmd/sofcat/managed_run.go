package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/facts"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/process"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/service"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
	"github.com/hurricanehrndz/sofcat/pkg/status"
)

var (
	adminCheckFunc = adminCheck
	mkdirAllFunc   = os.MkdirAll
	newReportFunc  = report.New
	// newZipInstaller builds the run's zip installer; tests swap in one with
	// temporary write roots.
	newZipInstaller = installer.NewMachineZip
)

// managedRun runs one full pass. progress receives per-item events and cancels
// lets the service withdraw items while the run is under way; both may be nil.
func managedRun(cfg config.Configuration, progress installer.ProgressFn, cancels *installer.Cancels) (_ *report.Report, runErr error) {
	// If not check-only, we need to run adminCheck().
	if !cfg.CheckOnly {
		admin, err := adminCheckFunc()
		if err != nil {
			return nil, fmt.Errorf("unable to check if running as admin: %w", err)
		}
		if !admin {
			return nil, errors.New("sofcat requires admnisistrative access. Please run as an administrator")
		}
	}

	// If needed, create the cache directory.
	if err := mkdirAllFunc(filepath.Clean(cfg.CachePath), 0o755); err != nil {
		return nil, fmt.Errorf("unable to create cache directory: %w", err)
	}

	// Create a new logger object
	if err := sofcatlog.NewLog(cfg); err != nil {
		return nil, fmt.Errorf("unable to initialize logger: %w", err)
	}

	// Build the run-scoped state: report + status checker (K7)
	run := newReportFunc()

	// The inventory is finished even when the run fails part way, so osquery
	// sees the failure; plan collects every item the run considers.
	plan := newPlanBuilder()
	plan.requestedBy = cfg.RequestedBy
	start := time.Now()
	defer func() { finishInventory(cfg, run, plan, start, runErr) }()

	// Set the configuration that `download` will use
	download.SetConfig(cfg)

	// Get the manifests
	slog.Info("Retrieving manifest", "manifest", cfg.Manifest)
	manifests, newCatalogs, err := manifest.Get(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve manifest: %w", err)
	}

	// If we have newCatalogs, add them to the configuration
	if newCatalogs != nil {
		cfg.Catalogs = append(cfg.Catalogs, newCatalogs...)
	}

	// Get the catalogs
	slog.Info("Retrieving catalog", "catalogs", cfg.Catalogs)
	catalogs, err := manifest.GetCatalogs(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve catalog: %w", err)
	}

	// The placement table for zip items; a repository without one has none.
	table, err := manifest.GetPlacements(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve placement table: %w", err)
	}

	// Decide which catalog items this machine may install (installable_condition).
	// The run refreshes the admin condition scripts' facts.
	machineFacts := facts.Collect(facts.Dir(cfg.AppDataPath), true)
	conditions.Apply(catalogs, machineFacts)
	plan.conditions = machineFacts

	// Resolve the manifests' conditional_items against the same facts. A block
	// whose condition names a missing fact holds its items: the run neither
	// installs nor removes them.
	manifests, held := manifest.Expand(manifests, machineFacts)
	for _, h := range held {
		slog.Warn("holding conditional items: facts unavailable", "condition", h.Condition, "missing", h.Missing, "items", h.Items)
		run.HeldConditions = append(run.HeldConditions, report.HeldCondition{Condition: h.Condition, Missing: h.Missing, Items: h.Items, Reason: "facts unavailable"})
	}

	// Process the manifests into install type groups
	slog.Info("Processing manifest...")
	installs, uninstalls, updates := process.Manifests(manifests, catalogs)
	plan.catalogs = catalogs
	plan.add(report.KindManagedInstall, false, installs...)
	plan.add(report.KindManagedUninstall, false, uninstalls...)
	plan.add(report.KindManagedUpdate, false, updates...)

	// Reconcile the self-serve manifest: assert once-only defaults, authorize
	// user selections against the admin optional_installs, and queue deselected
	// items for removal (R2, R4, R5).
	selfServePath := manifest.SelfServePath(cfg.AppDataPath)
	var selfServe manifest.Item
	var ssInstalls, ssUninstalls []string
	// Defaults are asserted on every run, including check-only (Munki asserts
	// during updatecheck), so save regardless of CheckOnly.
	err = manifest.UpdateSelfServe(selfServePath, func(entry *manifest.Item) bool {
		var changed bool
		ssInstalls, ssUninstalls, changed = process.ReconcileSelfServe(entry, manifests)
		selfServe = *entry
		return changed
	})
	if err != nil {
		return nil, fmt.Errorf("unable to reconcile self-serve manifest: %w", err)
	}
	installs = append(installs, ssInstalls...)
	uninstalls = append(uninstalls, ssUninstalls...)
	plan.add(report.KindManagedUninstall, true, ssUninstalls...)
	plan.addSelfServe(ssInstalls, selfServe)

	// Build the run-scoped installer context (K7). Zip items' status comes
	// from their receipts, for every status reader in the run.
	// A held item is never removed (not by managed_uninstalls, a pending
	// self-service removal, or scope exit), nor its managed flag rewritten.
	heldSet := heldItems(held)
	zip := newZipInstaller(cfg, table)
	zip.Facts = machineFacts
	managed := managedInstalls(manifests)
	zip.Managed = func(item string) bool { return managed[item] }
	zip.Held = func(item string) bool { return heldSet[item] }
	runner := &installer.Runner{
		Report:      run,
		Checker:     &status.Checker{Zip: zip.Installed, Nupkg: func(id string) (string, bool) { return installer.KeptNupkgVersion(cfg.NupkgPath, id) }},
		Emit:        progress,
		Cancels:     cancels,
		URLPackages: cfg.URLPackages,
		CachePath:   cfg.CachePath,
		NupkgPath:   cfg.NupkgPath,
		CheckOnly:   cfg.CheckOnly,
		Zip:         zip,
	}

	// Expand update_for (R7): build the updater index once, ride updaters of
	// referents merely installed on disk into the install list (referents being
	// installed this run are expanded in-walk), and couple updater removals to
	// their referent's removal.
	index := process.UpdaterIndex(catalogs)
	installsSet := make(map[string]bool, len(installs))
	for _, name := range installs {
		installsSet[name] = true
	}
	riders := process.InstalledReferentUpdaters(catalogs, index, installsSet, runner.Checker, cfg.CachePath)
	installs = append(installs, riders...)
	uninstalls = process.ExpandUninstallsWithUpdaters(uninstalls, index)
	plan.add(report.KindUpdateFor, false, riders...)
	plan.add(report.KindManagedUninstall, false, uninstalls...)
	plan.addUpdaters(index)

	uninstalls = dropHeld(uninstalls, heldSet)

	// Prepare and install
	slog.Info("Processing managed installs...")
	process.Installs(installs, catalogs, runner, index)

	// Prepare and uninstall
	slog.Info("Processing managed uninstalls...")
	process.Uninstalls(uninstalls, catalogs, runner)

	// Scope exit: remove receipt-tracked items that left the desired set.
	// The uninstall pass already handled (and reported) the items it could
	// resolve; one no catalog lists any more falls to scope exit.
	desired := desiredItems(slices.Concat(installs, updates), catalogs, index)
	removed := uninstallable(uninstalls, catalogs)
	slog.Info("Processing scope exit...")
	keep := func(item string) bool { return desired[item] || heldSet[item] || removed[item] }
	if err := runner.ScopeExit(keep); err != nil {
		slog.Warn("scope exit skipped", "err", err)
	}
	// Rewrite every marketplace's generated files from the receipts, so one
	// deleted or refused on an earlier run is restored.
	if !cfg.CheckOnly {
		runner.Regenerate()
	}

	// Prune self-serve uninstalls that are confirmed gone so the user can
	// reinstall later (R5). Only after a real run, never in check-only.
	if !cfg.CheckOnly {
		// Prune a fresh copy, not selfServe: a user cancel may have changed the
		// file since the run began, and saving the old copy would undo it.
		err := manifest.UpdateSelfServe(selfServePath, func(entry *manifest.Item) bool {
			return process.PruneSelfServeUninstalls(entry, catalogs, runner.Checker, cfg.CachePath)
		})
		if err != nil {
			return nil, fmt.Errorf("unable to save self-serve manifest after prune: %w", err)
		}
	}

	// Prepare and update
	slog.Info("Processing managed updates...")
	process.Updates(updates, catalogs, runner)
	plan.checkUpdates(run, runner.Checker, cfg.CachePath)

	// The service never writes into a user profile: it stages the archives
	// of user-scope placements for the per-user helper (--user-sync), then
	// stores the plan listUserPlacements serves. The plan comes from the
	// run's own desired and held sets, the ones scope exit used, with each
	// item deferred this run held too: the helper neither places nor removes
	// it, and its archive is not staged.
	if !cfg.CheckOnly {
		dir := service.UserArtifactsDir(cfg.AppDataPath)
		planDesired, planHeld := maps.Clone(desired), maps.Clone(heldSet)
		for _, d := range run.DeferredItems {
			delete(planDesired, d.Name)
			planHeld[d.Name] = true
		}
		userPlan := service.UserPlan(planDesired, planHeld, managed, catalogs, table, zip.Guard.GOOS, dir)
		service.StageUserArtifacts(context.Background(), zip, userPlan, dir)
		if err := service.SaveUserPlan(cfg.AppDataPath, service.UserPlacements{Entries: userPlan, Facts: machineFacts, Placements: service.UserSpecs(table, zip.Guard.GOOS)}); err != nil {
			slog.Warn("unable to store the user-placement plan", "err", err)
		}
	}

	// Offered optional installs the user has not selected still belong in the
	// inventory; the deferred finishInventory saves or prints it.
	plan.addAvailable(manifests, runner.Checker, cfg.CachePath)

	// Run CleanUp to delete old cached items and empty directories
	slog.Info("Cleaning up the cache...")
	process.CleanUp(cfg.CachePath)

	slog.Info("Done!")
	return run, nil
}
