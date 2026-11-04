package installer

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// errZipFailed is installZip's error when any placement failed; each one is
// already in the report with its own code.
var errZipFailed = errors.New("one or more placements failed")

// recordPlacement records a failed placement as its own FailedItem, naming the
// placement and carrying its code.
func (r *Runner) recordPlacement(item catalog.Item, action string, res Result) {
	r.Report.FailedItems = append(r.Report.FailedItems, report.FailedItem{
		Name:    item.Name,
		Version: item.Version,
		Action:  action,
		Error:   res.Err.Error(),
		Code:    res.Code(),
	})
}

// installZip is Install for a zip item. Its inspect step, not the status
// checker, decides whether anything needs doing. The requires_software and
// blocking-app gates and check-only mode apply as for any other item; pre- and
// post-scripts do not.
func (r *Runner) installZip(item catalog.Item, installerType string) (string, error) {
	if r.Zip == nil {
		err := fmt.Errorf("%w: no placement configuration for zip item %s", placement.ErrRefused, item.Name)
		r.recordPlacement(item, installerType, Result{Outcome: OutcomeRefused, Err: err})
		return "", err
	}
	switch installerType {
	case "install", "update":
		return r.placeZip(item, installerType)
	case "uninstall":
		return r.removeZip(item)
	default:
		return "", fmt.Errorf("unsupported item type %q for %s", installerType, item.DisplayName)
	}
}

func (r *Runner) noAction(item catalog.Item, installerType string) (string, error) {
	r.Report.NoActionItems = append(r.Report.NoActionItems, report.NoActionItem{
		Name:    item.Name,
		Version: item.Version,
		Action:  installerType,
	})
	return "Item not needed", nil
}

// pendingTargets returns the placements with work to do, and those among them
// that need their archive (an adoption, a conflict or a refusal needs none).
func (r *Runner) pendingTargets(item catalog.Item, machine []Target) ([]Target, map[string]bool) {
	var pending []Target
	needsArchive := map[string]bool{}
	for _, t := range machine {
		switch r.Zip.Inspect(item, t).Outcome { //nolint:exhaustive // the rest go to Apply, which reports them
		case OutcomeCurrent:
			continue
		case OutcomeMissing, OutcomeUnchanged:
			needsArchive[t.Placement] = true
		}
		pending = append(pending, t)
	}
	return pending, needsArchive
}

// noMachineWork ends an item with no machine placement to act on. The helper
// places its user-scope trees, but their requirement holds too: a deferral
// here keeps the item held in the user plan.
func (r *Runner) noMachineWork(item catalog.Item, installerType string, userScope bool) (string, error) {
	if userScope {
		if err := r.softwareGate(item, installerType); err != nil {
			return "", err
		}
	}
	return r.noAction(item, installerType)
}

func (r *Runner) placeZip(item catalog.Item, installerType string) (string, error) {
	machine, user := r.Zip.Targets(item)
	if len(user) > 0 {
		slog.Info("Skipping user-scope placements in a machine run", "item", item.DisplayName, "placements", strings.Join(user, ", "))
	}
	if installerType == "update" {
		// An update applies only to an item that is already placed somewhere.
		rcs, err := receipt.ForItem(r.Zip.ReceiptDir, item.Name)
		if err != nil {
			return "", r.recordFailure(item, installerType, fmt.Errorf("unable to read receipts: %w", err))
		}
		if len(rcs) == 0 {
			return r.noAction(item, installerType)
		}
	}

	pending, needsArchive := r.pendingTargets(item, machine)
	if len(pending) == 0 {
		return r.noMachineWork(item, installerType, len(user) > 0)
	}
	if r.Cancels.withdrawn(item.Name) {
		return "", r.canceled(item)
	}
	// Before any download or placement, check-only included.
	if err := r.softwareGate(item, installerType); err != nil {
		return "", err
	}
	if r.CheckOnly {
		r.Report.InstalledItems = append(r.Report.InstalledItems, item)
		slog.Info("[CHECK ONLY] Skipping actions", "item", item.DisplayName)
		return "Check only enabled", nil
	}
	if err := r.blockingGate(item, installerType); err != nil {
		return "", err
	}

	// Download every archive first, so a withdrawal can still abort it.
	ctx, done, ok := r.Cancels.download(item.Name)
	if !ok {
		return "", r.canceled(item)
	}
	r.emit(item, "downloading", 0, item.DisplayName)
	archives := make(map[string]string, len(pending))
	fetchErrs := make(map[string]error)
	for _, t := range pending {
		if needsArchive[t.Placement] {
			archives[t.Placement], fetchErrs[t.Placement] = r.Zip.Fetch(ctx, t)
		}
	}
	withdrawn := ctx.Err() != nil
	done()
	if withdrawn {
		return "", r.canceled(item)
	}
	if !r.Cancels.act(item.Name) {
		return "", r.canceled(item)
	}

	r.emit(item, "installing", 50, item.DisplayName)
	failed, placed := false, false
	for _, t := range pending {
		var res Result
		if err := fetchErrs[t.Placement]; err != nil {
			res = Result{Placement: t.Placement, Outcome: OutcomeFailed, Err: err}
		} else {
			res = r.Zip.Apply(item, t, archives[t.Placement])
		}
		if res.Failed() {
			slog.Warn("Placement FAILED", "item", item.DisplayName, "placement", t.Placement, "code", res.Code(), "err", res.Err)
			r.recordPlacement(item, installerType, res)
			failed = true
			continue
		}
		placed = placed || res.Outcome == OutcomeInstalled
	}
	if failed {
		r.emit(item, "failed", 100, errZipFailed.Error())
		return "", errZipFailed
	}
	if placed {
		r.Report.InstalledItems = append(r.Report.InstalledItems, item)
	} else {
		r.Report.NoActionItems = append(r.Report.NoActionItems, report.NoActionItem{Name: item.Name, Version: item.Version, Action: installerType})
	}
	r.emit(item, "done", 100, "")
	return "", nil
}

// removeZip removes every tree item's receipts record (R15).
func (r *Runner) removeZip(item catalog.Item) (string, error) {
	rcs, err := receipt.ForItem(r.Zip.ReceiptDir, item.Name)
	if err != nil {
		return "", r.recordFailure(item, "uninstall", fmt.Errorf("unable to read receipts: %w", err))
	}
	if len(rcs) == 0 {
		return r.noAction(item, "uninstall")
	}
	if r.Cancels.withdrawn(item.Name) {
		return "", r.canceled(item)
	}
	if r.CheckOnly {
		r.Report.InstalledItems = append(r.Report.InstalledItems, item)
		slog.Info("[CHECK ONLY] Skipping actions", "item", item.DisplayName)
		return "Check only enabled", nil
	}
	if err := r.blockingGate(item, "uninstall"); err != nil {
		return "", err
	}
	if !r.Cancels.act(item.Name) {
		return "", r.canceled(item)
	}
	r.emit(item, "removing", 0, item.DisplayName)
	if !r.removeReceipts(item, rcs) {
		r.emit(item, "failed", 100, errZipFailed.Error())
		return "", errZipFailed
	}
	r.emit(item, "done", 100, "")
	return "", nil
}

// removeReceipts removes each receipt's tree, recording failures per
// placement and the item as uninstalled when all succeeded.
func (r *Runner) removeReceipts(item catalog.Item, rcs []receipt.Receipt) bool {
	ok := true
	for _, rc := range rcs {
		res := r.Zip.Remove(rc)
		if res.Failed() {
			slog.Warn("Placement removal FAILED", "item", item.DisplayName, "placement", rc.Placement, "code", res.Code(), "err", res.Err)
			r.recordPlacement(item, "uninstall", res)
			ok = false
		}
	}
	if ok {
		r.Report.UninstalledItems = append(r.Report.UninstalledItems, item)
	}
	return ok
}

// ScopeExit removes, under R15, every receipt-tracked item that keep rejects:
// items that left the run's desired set and are not held. It runs after the
// install pass. Check-only runs remove nothing.
func (r *Runner) ScopeExit(keep func(item string) bool) error {
	if r.Zip == nil {
		return nil
	}
	all, err := receipt.List(r.Zip.ReceiptDir)
	if err != nil {
		return fmt.Errorf("unable to read receipts: %w", err)
	}
	byItem := map[string][]receipt.Receipt{}
	var order []string
	for _, rc := range all {
		if keep(rc.Item) {
			continue
		}
		if _, seen := byItem[rc.Item]; !seen {
			order = append(order, rc.Item)
		}
		byItem[rc.Item] = append(byItem[rc.Item], rc)
	}
	for _, name := range order {
		rcs := byItem[name]
		// The catalog may no longer list the item; the receipt names it.
		item := catalog.Item{Name: name, DisplayName: name, Version: rcs[0].Version}
		if r.CheckOnly {
			slog.Info("[CHECK ONLY] Would remove item that left the desired set", "item", name)
			continue
		}
		slog.Info("Removing item that left the desired set", "item", name)
		r.removeReceipts(item, rcs)
	}
	return nil
}

// Regenerate rewrites every marketplace's generated files from the receipts
// (ZipInstaller.RegenerateAll), recording each failure as a FailedItem named
// for the placement, with action generate.
func (r *Runner) Regenerate() {
	if r.Zip == nil {
		return
	}
	for _, res := range r.Zip.RegenerateAll() {
		slog.Warn("Regenerating placement FAILED", "placement", res.Placement, "code", res.Code(), "err", res.Err)
		r.Report.FailedItems = append(r.Report.FailedItems, report.FailedItem{
			Name:   res.Placement,
			Action: "generate",
			Error:  res.Err.Error(),
			Code:   res.Code(),
		})
	}
}
