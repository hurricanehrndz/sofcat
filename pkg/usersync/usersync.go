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
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/download"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/service"
)

// Client is the part of the service client the helper uses.
type Client interface {
	ListUserPlacements(context.Context) (service.UserPlacements, error)
	ReportUserPlacements(context.Context, []service.UserPlacementResult) error
}

// Sync applies the service's user-placement plan for one user. guard holds
// the OS (placement.Windows), the compiled write roots, whose user-scope roots
// apply, the refused data and binary directories, and Vars["USERPROFILE"].
// Receipts live in receiptDir. Held entries are neither placed nor removed.
// The trees' templates render with the plan's facts, each entry's managed
// flag and this account's login name; the helper runs no script of its own.
// After scope exit, the files of each placement the user holds receipts in
// are regenerated; a failure is a result with no item.
// Every result is logged and reported; the returned error is set only when
// the login name cannot be read, the lock cannot be taken or no plan could
// be fetched, after retrying a service that cannot be reached yet for up to
// PlanRetryFor, in which case nothing is placed or removed.
//
// One user's syncs run one at a time: a logon start, a start by the service
// and the same user in a second session can overlap. Sync first fetches the
// plan, with the retry, and only then waits for the lock file in receiptDir,
// so a helper with no service to answer it exits rather than waiting behind
// another sync. Under the lock it fetches the plan again, once, and applies
// that one, so a plan saved during another sync is never missed.
func Sync(ctx context.Context, client Client, guard placement.Guard, receiptDir string, logger *slog.Logger) ([]service.UserPlacementResult, error) {
	login, err := loginName()
	if err != nil {
		logger.Warn("user sync: unable to read the current user; nothing placed", "err", err)
		return nil, err
	}
	if _, err = listPlan(ctx, client, logger); err != nil {
		logger.Warn("user sync: unable to fetch the user-placement plan", "err", err)
		return nil, err
	}
	unlock, err := lock(receiptDir)
	if err != nil {
		logger.Warn("user sync: unable to take the per-user lock; nothing placed", "dir", receiptDir, "err", err)
		return nil, err
	}
	defer unlock()
	plan, err := client.ListUserPlacements(ctx)
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
	managed := map[string]bool{}
	for _, e := range plan.Entries {
		managed[e.Item] = managed[e.Item] || e.Managed
	}
	z := &installer.ZipInstaller{
		Guard:      guard,
		Table:      userTable(plan, receipts, guard.GOOS, logger),
		ReceiptDir: receiptDir,
		Managed:    func(item string) bool { return managed[item] },
		Facts:      plan.Facts,
		User:       login,
	}

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
	for _, e := range plan.Entries {
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

	// Regenerate the files of every placement this user still has items in,
	// so a file deleted or left stale is restored with no tree change. Not
	// every user-scope placement: its root, a tool's profile directory, can
	// exist with nothing of ours in it. Scope exit removed receipts, so they
	// are read again.
	if remaining, err := receipt.List(receiptDir); err != nil {
		logger.Warn("user sync: unable to read receipts; skipping placement files", "dir", receiptDir, "err", err)
	} else {
		var names []string
		for _, rc := range remaining {
			if !slices.Contains(names, rc.Placement) {
				names = append(names, rc.Placement)
			}
		}
		for _, res := range z.Regenerate(names) {
			record("", res.Placement, res)
		}
	}

	if err := client.ReportUserPlacements(ctx, results); err != nil {
		logger.Warn("user sync: unable to report results to the service", "err", err)
	}
	return results, nil
}

// The service may still be starting when the helper runs at logon: a
// connection failure is retried every planRetryInterval for PlanRetryFor.
// A helper the service started sets PlanRetryFor to 0: the service answered
// a moment ago, and a helper left waiting would hold its executable open
// through an uninstall. Tests replace wait so they need not sleep.
var (
	PlanRetryFor      = 60 * time.Second
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
func listPlan(ctx context.Context, client Client, logger *slog.Logger) (service.UserPlacements, error) {
	for waited := time.Duration(0); ; waited += planRetryInterval {
		plan, err := client.ListUserPlacements(ctx)
		if err == nil || !errors.Is(err, service.ErrConnect) || waited >= PlanRetryFor {
			return plan, err
		}
		logger.Info("user sync: service not reachable yet; retrying", "in", planRetryInterval, "err", err)
		if err := wait(ctx, planRetryInterval); err != nil {
			return service.UserPlacements{}, err
		}
	}
}

// lockName is the lock file in the receipt directory. receipt.List reads
// only its subdirectories, so the file is never taken for a receipt.
const lockName = ".sync.lock"

// lock blocks until this process holds receiptDir's lock file, creating
// both if needed. A file lock, not a mutex: a Local\ mutex is per session,
// and a Global\ one needs SeCreateGlobalPrivilege, which standard users lack.
// The lock goes with the process, so a crashed sync never strands it.
func lock(receiptDir string) (func(), error) {
	if err := os.MkdirAll(receiptDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(receiptDir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// currentUser is user.Current; tests replace it.
var currentUser = user.Current

// loginName is the login name of the account the helper runs as, for the
// tree templates' .User: user.Current's name without a DOMAIN\ prefix or an
// @domain suffix. When it cannot be read the sync fails before placing
// anything, rather than recording an empty user, and the next logon retries.
func loginName() (string, error) {
	u, err := currentUser()
	if err != nil {
		return "", fmt.Errorf("user sync: unable to read the current user: %w", err)
	}
	name := u.Username
	if i := strings.LastIndex(name, `\`); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	return name, nil
}

// apply places one plan entry, verifying the staged archive first whenever
// the tree has to be written from it.
func apply(z *installer.ZipInstaller, e service.UserPlacement) installer.Result {
	item := catalog.Item{Name: e.Item, DisplayName: e.Item, Version: e.Version}
	t := installer.Target{
		Placement: e.Placement,
		Variant:   catalog.Variant{Hash: e.Hash, TreeHash: e.TreeHash},
		Spec:      z.Table.Placements[e.Placement],
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

// userTable is the placement table the plan implies: the plan's spec of each
// user-scope placement, plus a synthesized user-scope directory placement,
// from its name and path alone, for a plan placement the plan has no spec
// for and for the placement of each receipt that left the plan, so their
// removal is checked against the user roots too. A plan from an older
// service carries no specs, so every placement is synthesized; exclusive,
// items_dir, vars and files then go unenforced until the service updates.
func userTable(plan service.UserPlacements, receipts []receipt.Receipt, goos string, logger *slog.Logger) placement.Table {
	table := placement.Table{Placements: map[string]placement.Spec{}}
	if plan.Placements == nil {
		logger.Debug("user sync: the plan carries no placement specs (older service); using paths only")
	}
	for name, spec := range plan.Placements {
		// The guard holds the machine roots too: a spec arriving with
		// machine scope must not pass it against them.
		spec.Scope = maps.Clone(spec.Scope)
		if spec.Scope == nil {
			spec.Scope = map[string]string{}
		}
		spec.Scope[goos] = placement.ScopeUser
		table.Placements[name] = spec
	}
	add := func(name, path string) {
		if _, ok := table.Placements[name]; ok {
			return
		}
		p := placement.Spec{Scope: map[string]string{goos: placement.ScopeUser}}
		if path != "" {
			p.Paths = map[string]string{goos: path}
		}
		table.Placements[name] = p
	}
	for _, e := range plan.Entries {
		add(e.Placement, e.Path)
	}
	for _, rc := range receipts {
		add(rc.Placement, "")
	}
	table.Order = slices.Sorted(maps.Keys(table.Placements))
	return table
}
