package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/report"
)

// helperWaitRig runs the service with the user-scope item "Skill" (codex)
// and a managed run that, once armed, saves the given plan and, with
// startHelper, records a helper start for the test's own user, as
// StartUserSync does for a signed-in user. ran signals each run.
type helperWaitRig struct {
	cfg    config.Configuration
	sr     *serviceRunner
	ctx    context.Context
	client *Client
	me     string
	next   atomic.Pointer[[]UserPlacement]
	start  atomic.Bool
	ran    chan struct{}
}

func newHelperWaitRig(t *testing.T, planned ...string) *helperWaitRig {
	t.Helper()
	cfg := testServiceConfig(t)
	cfg.CachePath, cfg.Catalogs = t.TempDir(), []string{"production"}
	stubUserScopeList(t, cfg)
	saveStatusPlan(t, cfg.AppDataPath, planPairs(planned)...)
	me, err := connectedPeer(t)
	if err != nil || me.ID == "" {
		t.Fatalf("resolve the test's user: %+v, %v", me, err)
	}
	rig := &helperWaitRig{cfg: cfg, me: me.ID, ran: make(chan struct{}, 4)}
	rig.sr, rig.ctx = startTestRunner(t, cfg, func(in config.Configuration, _ installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		entries := rig.next.Swap(nil)
		if entries == nil {
			rig.ran <- struct{}{}
			return nil, nil
		}
		if err := SaveUserPlan(in.AppDataPath, UserPlacements{Entries: *entries}); err != nil {
			return nil, err
		}
		if rig.start.Load() {
			noteUserSyncStarted(rig.me)
		}
		rig.ran <- struct{}{}
		return nil, nil
	})
	rig.client = NewClient(cfg.ServicePipeName)
	// The start-up run must not take the plan a test arms.
	<-rig.ran
	return rig
}

func planPairs(placements []string) [][2]string {
	var pairs [][2]string
	for _, p := range placements {
		pairs = append(pairs, [2]string{"Skill", p})
	}
	return pairs
}

// arm makes the next run save a plan listing Skill in placements.
func (r *helperWaitRig) arm(startHelper bool, placements ...string) {
	entries := []UserPlacement{}
	for _, p := range placements {
		entries = append(entries, UserPlacement{Item: "Skill", Placement: p})
	}
	r.start.Store(startHelper)
	r.next.Store(&entries)
}

func (r *helperWaitRig) mutate(t *testing.T, remove bool) string {
	t.Helper()
	call := r.client.InstallItem
	if remove {
		call = r.client.RemoveItem
	}
	accepted, err := call(r.ctx, "Skill", "")
	if err != nil {
		t.Fatalf("mutation: %v", err)
	}
	select {
	case <-r.ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the operation's run never ran")
	}
	return accepted.OperationID
}

// awaitState waits for the operation's latest record to be state.
func (r *helperWaitRig) awaitState(t *testing.T, id, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		events, done, _ := r.sr.snapshotTrackedOperation(id)
		if len(events) > 0 && events[len(events)-1].State == state {
			if done != IsTerminalOperationState(state) {
				t.Fatalf("%s record with done=%v", state, done)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation never reached %s: %+v", state, events)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *helperWaitRig) report(t *testing.T, results ...UserPlacementResult) {
	t.Helper()
	if err := r.client.ReportUserPlacements(r.ctx, results); err != nil {
		t.Fatalf("ReportUserPlacements: %v", err)
	}
}

func (r *helperWaitRig) listed(t *testing.T) string {
	t.Helper()
	items, err := r.client.ListOptionalInstalls(r.ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListOptionalInstalls = %+v, %v", items, err)
	}
	return items[0].Status
}

// The storefront reloads its list when an operation ends. A user-scope item
// is placed by the user's helper after the run, so the operation must stay
// open until that helper reports, or the reload still reads "Will be added".
func TestOperationWaitsForTheUserHelper(t *testing.T) {
	shortenTimeout(t, &userHelperWait, time.Minute)
	rig := newHelperWaitRig(t)
	rig.arm(true, "codex")
	id := rig.mutate(t, false)

	rig.awaitState(t, id, "Installing")
	// The run is over, so a cancel could only revert a choice already acted on.
	if err := rig.client.CancelOperation(rig.ctx, id); !IsErrorCode(err, "operation_not_cancelable") {
		t.Fatalf("cancel during the wait = %v, want operation_not_cancelable", err)
	}
	rig.report(t, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "installed"})
	if terminal := mustStreamTerminal(t, rig.cfg, id); terminal.State != "Succeeded" {
		t.Fatalf("terminal = %+v, want Succeeded", terminal)
	}
	if got := rig.listed(t); got != "Installed" {
		t.Errorf("list at the operation's end = %s, want Installed", got)
	}
}

// A removal waits for the helper to take the tree away; a report from a
// helper that fetched the old plan, still reading it placed, does not count.
func TestRemoveWaitsForTheUserHelper(t *testing.T) {
	shortenTimeout(t, &userHelperWait, time.Minute)
	rig := newHelperWaitRig(t, "codex")
	rig.report(t, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "installed"})
	rig.arm(false, "codex")
	rig.mutate(t, false) // selects Skill, so this user may remove it
	rig.arm(true)
	id := rig.mutate(t, true)

	rig.awaitState(t, id, "Removing")
	rig.report(t, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "current"})
	rig.awaitState(t, id, "Removing")
	rig.report(t, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "removed"})
	if terminal := mustStreamTerminal(t, rig.cfg, id); terminal.State != "Succeeded" {
		t.Fatalf("terminal = %+v, want Succeeded", terminal)
	}
}

// A helper that fails to place the tree fails the operation, with the code a
// machine-scope placement failure carries, so the card says why the row is
// not installed.
func TestOperationFailsOnAFailedHelperReport(t *testing.T) {
	shortenTimeout(t, &userHelperWait, time.Minute)
	rig := newHelperWaitRig(t)
	rig.arm(true, "codex")
	id := rig.mutate(t, false)
	rig.awaitState(t, id, "Installing")
	rig.report(t, UserPlacementResult{Item: "Skill", Placement: "codex", Outcome: "conflict", Error: "edited outside sofcat"})
	terminal := mustStreamTerminal(t, rig.cfg, id)
	if terminal.State != "Failed" || terminal.ErrorCode != "placement_conflict" || terminal.ErrorMessage != "edited outside sofcat" {
		t.Fatalf("terminal = %+v, want Failed/placement_conflict", terminal)
	}
}

// A helper that never reports must not hold the operation open.
func TestOperationEndsWhenTheUserHelperIsSilent(t *testing.T) {
	shortenTimeout(t, &userHelperWait, 50*time.Millisecond)
	rig := newHelperWaitRig(t)
	rig.arm(true, "codex")
	id := rig.mutate(t, false)
	if terminal := mustStreamTerminal(t, rig.cfg, id); terminal.State != "Succeeded" {
		t.Fatalf("terminal = %+v, want Succeeded", terminal)
	}
}

// No wait where no report can come: the run did not change the item's user
// placements, or started no helper for the caller (no signed-in session, no
// helper installed, a failed start).
func TestOperationDoesNotWaitWithoutAHelperToReport(t *testing.T) {
	for _, tc := range []struct {
		name    string
		planned []string
		start   bool
	}{
		{"unchanged plan", []string{"codex"}, true},
		{"no helper started", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortenTimeout(t, &userHelperWait, time.Hour)
			rig := newHelperWaitRig(t, tc.planned...)
			rig.arm(tc.start, "codex")
			id := rig.mutate(t, false)
			done := make(chan OperationStatus, 1)
			go func() {
				var last OperationStatus
				err := rig.client.StreamOperationStatus(rig.ctx, id, func(s OperationStatus) error {
					if s.State == "Installing" {
						return errors.New("the operation waited for a helper")
					}
					last = s
					return nil
				})
				if err != nil {
					last = OperationStatus{State: err.Error()}
				}
				done <- last
			}()
			select {
			case terminal := <-done:
				if terminal.State != "Succeeded" {
					t.Fatalf("terminal = %+v, want Succeeded with no wait", terminal)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the operation is waiting for a helper")
			}
		})
	}
}
