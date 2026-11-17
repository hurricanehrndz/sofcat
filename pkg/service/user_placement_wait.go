package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/installer"
)

// userHelperWait bounds how long an install or remove operation waits for
// the requester's per-user helper to report on the user-scope placements its
// run changed. The helper starts, fetches the plan twice, unpacks a small
// tree and reports, which takes a few seconds; the margin covers an antivirus
// scan of the unpacked files and a helper waiting on the per-user lock behind
// a logon sync. Past it the operation ends as the run left it. Tests shorten
// it.
var userHelperWait = 30 * time.Second

// userSyncStarts counts the helpers StartUserSync started in this process,
// by user ID (the SID on Windows, the uid on macOS), so an operation waits
// only for a helper that is running for its requester. It grows by one key
// per user ever signed in while the service runs.
var userSyncStarts = struct {
	sync.Mutex
	n map[string]uint64
}{n: map[string]uint64{}}

// noteUserSyncStarted records that StartUserSync started the helper of the
// user with this ID.
func noteUserSyncStarted(id string) {
	userSyncStarts.Lock()
	defer userSyncStarts.Unlock()
	userSyncStarts.n[id]++
}

func userSyncStartCount(id string) uint64 {
	userSyncStarts.Lock()
	defer userSyncStarts.Unlock()
	return userSyncStarts.n[id]
}

// userReportWatch collects the reportUserPlacements results from one user
// while an operation they requested runs and waits.
type userReportWatch struct {
	id      string
	mu      sync.Mutex
	results []UserPlacementResult
	// arrived has room for one signal: a report that lands while the waiter
	// is busy is still seen at its next take.
	arrived chan struct{}
}

// take returns the results collected since the last take.
func (w *userReportWatch) take() []UserPlacementResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.results
	w.results = nil
	return out
}

// watchUserReports starts collecting the reports of the user with this ID.
// It returns nil for an unresolved caller, whose reports cannot be told apart.
func (sr *serviceRunner) watchUserReports(id string) *userReportWatch {
	if id == "" {
		return nil
	}
	w := &userReportWatch{id: id, arrived: make(chan struct{}, 1)}
	sr.reportWatchMu.Lock()
	defer sr.reportWatchMu.Unlock()
	if sr.reportWatches == nil {
		sr.reportWatches = map[*userReportWatch]struct{}{}
	}
	sr.reportWatches[w] = struct{}{}
	return w
}

func (sr *serviceRunner) unwatchUserReports(w *userReportWatch) {
	if w == nil {
		return
	}
	sr.reportWatchMu.Lock()
	defer sr.reportWatchMu.Unlock()
	delete(sr.reportWatches, w)
}

// publishUserReport hands one helper's results to every watch on its user.
// dispatchUserPlacements calls it after storing them, so an operation that
// ends on it is followed by a list that already reads them.
func (sr *serviceRunner) publishUserReport(id string, results []UserPlacementResult) {
	if id == "" {
		return
	}
	sr.reportWatchMu.Lock()
	defer sr.reportWatchMu.Unlock()
	for w := range sr.reportWatches {
		if w.id != id {
			continue
		}
		w.mu.Lock()
		w.results = append(w.results, results...)
		w.mu.Unlock()
		select {
		case w.arrived <- struct{}{}:
		default:
		}
	}
}

// userHelperBaseline is what an operation's run is compared with, all read
// before the run: the item's entries in the stored plan by placement, what
// the requester last reported for it, and how many helpers had been started
// for them. watch is nil when the requester is unresolved.
type userHelperBaseline struct {
	entries  map[string]UserPlacement
	reported map[string]userPlacementOutcome
	starts   uint64
	watch    *userReportWatch
}

// userPlanEntries is the item's entries in the stored plan, by placement.
func userPlanEntries(appDataPath, itemName string) (map[string]UserPlacement, error) {
	plan, err := LoadUserPlan(appDataPath)
	if err != nil {
		return nil, err
	}
	entries := map[string]UserPlacement{}
	for _, e := range plan.Entries {
		if e.Item == itemName {
			entries[e.Placement] = e
		}
	}
	return entries, nil
}

// newUserHelperBaseline starts watching the requester's reports and reads the
// baseline. The caller unwatches base.watch once the operation has ended.
func (sr *serviceRunner) newUserHelperBaseline(itemName, requestedByID string) userHelperBaseline {
	watch := sr.watchUserReports(requestedByID)
	if watch == nil {
		return userHelperBaseline{}
	}
	appData := sr.config().AppDataPath
	entries, err := userPlanEntries(appData, itemName)
	if err != nil {
		slog.Warn("unable to read the user plan before a run", "item", itemName, "err", err)
	}
	return userHelperBaseline{
		entries:  entries,
		reported: userPlacementStatusFor(appData, requestedByID)[itemName],
		starts:   userSyncStartCount(requestedByID),
		watch:    watch,
	}
}

// changedUserEntries returns the item's user-scope placements the run changed
// for the requester, each mapped to whether the helper is to place it (true)
// or remove it (false). An entry is changed when the plan gained it, or it
// differs from before (an update, a held entry released). A held entry is
// left out, since the helper neither places nor removes it and so does not
// report it, and so is a dropped entry the requester had no placed tree for.
func changedUserEntries(base userHelperBaseline, after map[string]UserPlacement) map[string]bool {
	changed := map[string]bool{}
	for name, e := range after {
		if old, ok := base.entries[name]; !e.Held && (!ok || old != e) {
			changed[name] = true
		}
	}
	for name := range base.entries {
		if _, ok := after[name]; !ok && placedOutcomes[installer.Outcome(base.reported[name].Outcome)] {
			changed[name] = false
		}
	}
	return changed
}

// awaitUserHelper holds back the terminal record of an install or remove
// operation that succeeded until the requester's helper has reported on
// every user-scope placement the run changed for them, so the list the
// storefront reloads at the end already reads the outcome. It waits only
// when the run started that helper (StartUserSync); a requester with no
// signed-in session, a machine without the helper, or one whose start
// failed gets the run's record at once. The wait is bounded by
// userHelperWait and by ctx. While it lasts the operation reads Installing
// or Removing, and cancelOperation refuses it, since the run has acted.
//
// A result that places a wanted entry or removes a dropped one settles it.
// One with an error fails the operation, as a machine-scope placement
// failure does, so the card says why the row is not installed. Results for
// a dropped entry that still read placed come from a helper that fetched the
// old plan and are passed over.
func (sr *serviceRunner) awaitUserHelper(ctx context.Context, operationID, itemName string, base userHelperBaseline, terminal OperationStatus) OperationStatus {
	if base.watch == nil || terminal.State != "Succeeded" {
		return terminal
	}
	after, err := userPlanEntries(sr.config().AppDataPath, itemName)
	if err != nil {
		slog.Warn("unable to read the user plan after a run", "item", itemName, "err", err)
		return terminal
	}
	pending := changedUserEntries(base, after)
	if len(pending) == 0 || userSyncStartCount(base.watch.id) == base.starts {
		return terminal
	}
	phase := "Removing"
	for _, place := range pending {
		if place {
			phase = "Installing"
		}
	}
	if !sr.markAwaitingHelper(operationID, phase) {
		return terminal
	}

	timer := time.NewTimer(userHelperWait)
	defer timer.Stop()
	for {
		for _, r := range base.watch.take() {
			place, ok := pending[r.Placement]
			if !ok || r.Item != itemName {
				continue
			}
			if r.Error != "" {
				return OperationStatus{
					State:           "Failed",
					ProgressPercent: new(100),
					Message:         "Operation failed",
					ErrorCode:       orDefault(installer.Result{Outcome: installer.Outcome(r.Outcome)}.Code(), "item_failed"),
					ErrorMessage:    r.Error,
				}
			}
			if !place && placedOutcomes[installer.Outcome(r.Outcome)] {
				continue
			}
			delete(pending, r.Placement)
		}
		if len(pending) == 0 {
			return terminal
		}
		select {
		case <-base.watch.arrived:
		case <-timer.C:
			slog.Warn("the user's helper did not report in time; ending the operation without it",
				"operationId", operationID, "item", itemName, "requestedByID", base.watch.id, "wait", userHelperWait.String())
			return terminal
		case <-ctx.Done():
			return terminal
		}
	}
}

// markAwaitingHelper records the operation's wait for the helper with a
// phase record naming the item. It reports false when the operation has
// already ended, a user cancel that landed as the run finished.
func (sr *serviceRunner) markAwaitingHelper(operationID, phase string) bool {
	sr.operationsMu.Lock()
	defer sr.operationsMu.Unlock()
	op, ok := sr.operations[operationID]
	if !ok || op.done {
		return false
	}
	op.awaitingHelper = true
	sr.appendOperationEventLocked(operationID, OperationStatus{State: phase, Message: op.requestedDisplayName})
	return true
}
