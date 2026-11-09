package service

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
)

// maxUserPlacementStatusUsers bounds the users user-placement-status.json
// keeps; past it the user whose latest report is oldest is dropped, and
// their cards fall back to the machine view until their agent reports again.
const maxUserPlacementStatusUsers = 64

// userPlacementOutcome is a user agent's last outcome for one placement of
// one item, an installer.Outcome, and when the service stored it.
type userPlacementOutcome struct {
	Outcome string    `json:"outcome"`
	At      time.Time `json:"at"`
}

// userPlacementStatus is user-placement-status.json: the outcome each user
// agent last reported, by the caller's SID (the token's), item and
// placement. The storefront's status for user-scope items comes from it.
// The service never reads the user's own receipts: they are files the user
// can write, and opening them as SYSTEM on the command worker would let a
// user stall the service or point it at another user's files.
type userPlacementStatus map[string]map[string]map[string]userPlacementOutcome

// userPlacementStatusMu serializes the file's read-merge-write: reports
// arrive on their own connections, off the command queue, while the queue
// reads it for ListOptionalInstalls.
var userPlacementStatusMu sync.Mutex

func userPlacementStatusPath(appDataPath string) string {
	return filepath.Join(appDataPath, "user-placement-status.json")
}

// placedOutcomes are the outcomes after which the user's tree is in place.
// Removed, missing, refused and failed leave it absent; conflict says
// nothing new and is not stored.
var placedOutcomes = map[installer.Outcome]bool{
	installer.OutcomeInstalled: true,
	installer.OutcomeCurrent:   true,
	installer.OutcomeAdopted:   true,
	installer.OutcomeUnchanged: true,
	installer.OutcomeRemoved:   false,
	installer.OutcomeMissing:   false,
	installer.OutcomeRefused:   false,
	installer.OutcomeFailed:    false,
}

// loadUserPlacementStatus reads the file; a missing or corrupt one is empty,
// since the next reports rebuild it. The caller holds userPlacementStatusMu.
func loadUserPlacementStatus(appDataPath string) userPlacementStatus {
	status := userPlacementStatus{}
	data, err := os.ReadFile(userPlacementStatusPath(appDataPath))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("unable to read user placement status", "err", err)
		}
		return status
	}
	if err := json.Unmarshal(data, &status); err != nil || status == nil {
		slog.Warn("ignoring unreadable user placement status", "err", err)
		return userPlacementStatus{}
	}
	return status
}

// recordUserPlacementStatus merges one report from the user with this SID
// into the file. A result counts only for an item and placement the stored
// plan lists, with a known outcome other than conflict; held items are not
// reported, so the user's earlier entries stand. Entries the plan no longer
// lists are pruned for every user. An empty SID stores nothing.
func recordUserPlacementStatus(appDataPath, sid string, results []UserPlacementResult, now time.Time) error {
	if sid == "" {
		return nil
	}
	userPlacementStatusMu.Lock()
	defer userPlacementStatusMu.Unlock()
	inPlan, err := userPlanPairs(appDataPath)
	if err != nil {
		return err
	}
	status := loadUserPlacementStatus(appDataPath)
	mine := status[sid]
	if mine == nil {
		mine = map[string]map[string]userPlacementOutcome{}
	}
	for _, r := range results {
		if _, known := placedOutcomes[installer.Outcome(r.Outcome)]; !known || r.Item == "" || !inPlan[[2]string{r.Item, r.Placement}] {
			continue
		}
		if mine[r.Item] == nil {
			mine[r.Item] = map[string]userPlacementOutcome{}
		}
		mine[r.Item][r.Placement] = userPlacementOutcome{Outcome: r.Outcome, At: now}
	}
	status[sid] = mine

	for user, items := range status {
		for item, placements := range items {
			for name := range placements {
				if !inPlan[[2]string{item, name}] {
					delete(placements, name)
				}
			}
			if len(placements) == 0 {
				delete(items, item)
			}
		}
		if len(items) == 0 {
			delete(status, user)
		}
	}
	for len(status) > maxUserPlacementStatusUsers {
		delete(status, oldestUser(status))
	}

	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(appDataPath, 0o755); err != nil {
		return err
	}
	// Only the service reads it: SYSTEM-only, as the data directory is.
	return writeFileAtomic(userPlacementStatusPath(appDataPath), 0o600, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// oldestUser is the SID whose latest stored outcome is the oldest.
func oldestUser(status userPlacementStatus) string {
	var oldest string
	var oldestAt time.Time
	for user, items := range status {
		var latest time.Time
		for _, placements := range items {
			for _, o := range placements {
				if o.At.After(latest) {
					latest = o.At
				}
			}
		}
		if oldest == "" || latest.Before(oldestAt) || (latest.Equal(oldestAt) && user < oldest) {
			oldest, oldestAt = user, latest
		}
	}
	return oldest
}

// userPlanPairs is the item/placement pairs the stored plan lists.
func userPlanPairs(appDataPath string) (map[[2]string]bool, error) {
	plan, err := LoadUserPlan(appDataPath)
	if err != nil {
		return nil, err
	}
	inPlan := map[[2]string]bool{}
	for _, e := range plan.Entries {
		inPlan[[2]string{e.Item, e.Placement}] = true
	}
	return inPlan, nil
}

// userPlacementStatusFor is what the user with this SID last reported, by
// item and placement, for what the stored plan still lists: a run that drops
// an item saves its plan before the agent reports the removal, if it ever
// does. Nil for an empty SID or an unreadable plan.
func userPlacementStatusFor(appDataPath, sid string) map[string]map[string]userPlacementOutcome {
	if sid == "" {
		return nil
	}
	userPlacementStatusMu.Lock()
	defer userPlacementStatusMu.Unlock()
	inPlan, err := userPlanPairs(appDataPath)
	if err != nil {
		slog.Warn("unable to read the user plan for user placement status", "err", err)
		return nil
	}
	mine := loadUserPlacementStatus(appDataPath)[sid]
	for item, placements := range mine {
		for name := range placements {
			if !inPlan[[2]string{item, name}] {
				delete(placements, name)
			}
		}
		if len(placements) == 0 {
			delete(mine, item)
		}
	}
	return mine
}

// userZipInstalled is the storefront's zip status for one user, from what
// their agent reported (reported): an item with user-scope placements is
// installed when its machine-scope placements are (or it has none) and the
// user's agent placed every user-scope one. An item the user has no entry
// for reads as the machine view, zip.Installed, as does every item when
// nothing was reported: no SID, or an OS with no user agent.
func userZipInstalled(zip *installer.ZipInstaller, reported map[string]map[string]userPlacementOutcome) func(catalog.Item) bool {
	if len(reported) == 0 {
		return zip.Installed
	}
	return func(item catalog.Item) bool {
		machine, user := zip.Targets(item)
		placements, ok := reported[item.Name]
		if !ok || len(user) == 0 {
			return zip.Installed(item)
		}
		if len(machine) > 0 && !zip.Installed(item) {
			return false
		}
		for _, name := range user {
			if !placedOutcomes[installer.Outcome(placements[name].Outcome)] {
				return false
			}
		}
		return true
	}
}
