// Package usersync is the per-user helper's work (`<slug>-ui.exe
// --user-sync`): it asks the service for the user-placement plan, places
// each tree into the caller's own profile from the archive the service
// staged, removes the trees its own receipts track that left the plan, and
// reports every result back. It runs as the user, never as SYSTEM, and is
// portable so it is tested on any host.
package usersync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/harness"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/service"
)

// Client is the part of the service client the helper uses.
type Client interface {
	ListUserPlacements(context.Context) ([]service.UserPlacement, error)
	ReportUserPlacements(context.Context, []service.UserPlacementResult) error
}

// Sync applies the service's user-placement plan for one user. guard holds
// the OS (harness.Windows), the compiled write roots, whose user-scope roots
// apply, the refused data and binary directories, and Vars["USERPROFILE"].
// Receipts live in receiptDir. Held entries are neither placed nor removed.
// Every result is logged and reported; the returned error is set only when
// no plan could be fetched, after retrying a service that cannot be reached
// yet for up to a minute, in which case nothing is removed.
//
// Two syncs for one user at once (a logon and a storefront start) can trip
// over each other's staging directory; the loser logs a failed result and
// the next sync converges. A per-user lock is the upgrade if that shows up.
func Sync(ctx context.Context, client Client, guard placement.Guard, receiptDir string, logger *slog.Logger) ([]service.UserPlacementResult, error) {
	plan, err := listPlan(ctx, client, logger)
	if err != nil {
		logger.Warn("user sync: unable to fetch the user-placement plan", "err", err)
		return nil, err
	}
	receipts, err := receipt.List(receiptDir)
	if err != nil {
		// Without the receipts, scope exit cannot tell what is ours; placing
		// still follows R14, which reads each receipt itself.
		logger.Warn("user sync: unable to read receipts; skipping scope exit", "dir", receiptDir, "err", err)
	}
	z := &installer.ZipInstaller{Guard: guard, Harnesses: userTable(plan, receipts, guard.GOOS), ReceiptDir: receiptDir}

	var results []service.UserPlacementResult
	record := func(item, placementName string, res installer.Result) {
		r := service.UserPlacementResult{Item: item, Placement: placementName, Outcome: string(res.Outcome)}
		if res.Err != nil {
			r.Error = res.Err.Error()
			logger.Warn("user sync: placement failed", "item", item, "placement", placementName, "outcome", res.Outcome, "path", res.Path, "err", res.Err)
		} else {
			logger.Info("user sync: placement done", "item", item, "placement", placementName, "outcome", res.Outcome, "path", res.Path)
		}
		results = append(results, r)
	}

	inPlan := map[[2]string]bool{}
	held := map[string]bool{}
	for _, e := range plan {
		inPlan[[2]string{e.Item, e.Placement}] = true
		if e.Held {
			held[e.Item] = true
			logger.Info("user sync: leaving held item alone", "item", e.Item, "placement", e.Placement)
			continue
		}
		record(e.Item, e.Placement, apply(z, e))
	}

	// Scope exit over this user's own receipts.
	for _, rc := range receipts {
		if inPlan[[2]string{rc.Item, rc.Placement}] || held[rc.Item] {
			continue
		}
		record(rc.Item, rc.Placement, z.Remove(rc))
	}

	if err := client.ReportUserPlacements(ctx, results); err != nil {
		logger.Warn("user sync: unable to report results to the service", "err", err)
	}
	return results, nil
}

// The service may still be starting when the helper runs at logon: a
// connection failure is retried every planRetryInterval for planRetryFor.
// Tests replace wait so they need not sleep.
var (
	planRetryFor      = 60 * time.Second
	planRetryInterval = 5 * time.Second
	wait              = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
)

// listPlan fetches the plan, retrying only while the service cannot be
// reached; any other error, such as one the service answered, is returned
// at once.
func listPlan(ctx context.Context, client Client, logger *slog.Logger) ([]service.UserPlacement, error) {
	for waited := time.Duration(0); ; waited += planRetryInterval {
		plan, err := client.ListUserPlacements(ctx)
		if err == nil || !errors.Is(err, service.ErrConnect) || waited >= planRetryFor {
			return plan, err
		}
		logger.Info("user sync: service not reachable yet; retrying", "in", planRetryInterval, "err", err)
		if err := wait(ctx, planRetryInterval); err != nil {
			return nil, err
		}
	}
}

// apply places one plan entry, verifying the staged archive first whenever
// the tree has to be written from it.
func apply(z *installer.ZipInstaller, e service.UserPlacement) installer.Result {
	item := catalog.Item{Name: e.Item, DisplayName: e.Item, Version: e.Version}
	t := installer.Target{
		Placement: e.Placement,
		Variant:   catalog.Variant{Hash: e.Hash, TreeHash: e.TreeHash},
		Spec:      z.Harnesses.Placements[e.Placement],
		Known:     true,
	}
	res := z.Inspect(item, t)
	switch res.Outcome { //nolint:exhaustive // Apply reports every other outcome without reading the archive
	case installer.OutcomeMissing, installer.OutcomeUnchanged:
		if !download.Verify(e.Archive, e.Hash) {
			res.Outcome = installer.OutcomeFailed
			res.Err = fmt.Errorf("placement %q: staged archive %s is missing or does not match its hash", e.Placement, e.Archive)
			return res
		}
	}
	return z.Apply(item, t, e.Archive)
}

// userTable is the harness table the plan implies: one user-scope directory
// placement per plan placement, plus the placements of receipts that left
// the plan, so their removal is checked against the user roots too.
func userTable(plan []service.UserPlacement, receipts []receipt.Receipt, goos string) harness.Table {
	table := harness.Table{Placements: map[string]harness.Placement{}}
	add := func(name, path string) {
		if _, ok := table.Placements[name]; ok {
			return
		}
		p := harness.Placement{Kind: harness.KindDirectory, Scope: map[string]string{goos: harness.ScopeUser}}
		if path != "" {
			p.Paths = map[string]string{goos: path}
		}
		table.Placements[name] = p
		table.Order = append(table.Order, name)
	}
	for _, e := range plan {
		add(e.Placement, e.Path)
	}
	for _, rc := range receipts {
		add(rc.Placement, "")
	}
	return table
}
