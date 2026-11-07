package usersync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/artifact"
	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"github.com/hurricanehrndz/sofcat/pkg/installer"
	"github.com/hurricanehrndz/sofcat/pkg/placement"
	"github.com/hurricanehrndz/sofcat/pkg/receipt"
	"github.com/hurricanehrndz/sofcat/pkg/service"
)

type fakeClient struct {
	plan  []service.UserPlacement
	facts conditions.Facts
	err   error
	// failures are returned, in order, before plan or err.
	failures []error
	calls    int
	reported []service.UserPlacementResult
}

func (c *fakeClient) ListUserPlacements(context.Context) (service.UserPlacements, error) {
	c.calls++
	if len(c.failures) > 0 {
		err := c.failures[0]
		c.failures = c.failures[1:]
		return service.UserPlacements{}, err
	}
	return service.UserPlacements{Entries: c.plan, Facts: c.facts}, c.err
}

func (c *fakeClient) ReportUserPlacements(_ context.Context, r []service.UserPlacementResult) error {
	c.reported = r
	return nil
}

// fixture packs a one-file skill and returns a plan entry for it in the
// placement "codex" under ${USERPROFILE}/.agents/skills, plus a guard for a
// temporary profile. Windows keys work on any host.
func fixture(t *testing.T) (service.UserPlacement, placement.Guard, string) {
	t.Helper()
	return fixtureTree(t, map[string]string{"SKILL.md": "skill"})
}

// fixtureTree is fixture with the skill's files.
func fixtureTree(t *testing.T, files map[string]string) (service.UserPlacement, placement.Guard, string) {
	t.Helper()
	src := t.TempDir()
	for name, body := range files {
		path := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "skill.zip")
	packed, err := artifact.PackFile(src, placement.Windows, archive)
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	guard := placement.Guard{
		GOOS:  placement.Windows,
		Roots: placement.Roots{WindowsUser: []string{"${USERPROFILE}/.agents"}},
		Vars:  map[string]string{"USERPROFILE": profile},
	}
	entry := service.UserPlacement{
		Item: "skill", Version: "1.0", Placement: "codex", Path: "${USERPROFILE}/.agents/skills",
		Archive: archive, Hash: packed.Hash, TreeHash: packed.TreeHash,
	}
	return entry, guard, profile
}

// A staged archive that no longer matches the plan's hash is not placed.
func TestSyncRefusesUnverifiedArchive(t *testing.T) {
	entry, guard, profile := fixture(t)
	if err := os.WriteFile(entry.Archive, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{plan: []service.UserPlacement{entry}}
	results, err := Sync(context.Background(), client, guard, filepath.Join(t.TempDir(), "receipts"), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Outcome != string(installer.OutcomeFailed) || results[0].Error == "" {
		t.Errorf("results = %+v, want one failure", results)
	}
	if _, err := os.Stat(filepath.Join(profile, ".agents")); !os.IsNotExist(err) {
		t.Errorf("something was written under the profile: %v", err)
	}
	if len(client.reported) != 1 {
		t.Errorf("reported %+v", client.reported)
	}
}

// Without a plan the helper removes nothing, even trees whose receipts would
// otherwise be out of scope.
func TestSyncWithoutPlanRemovesNothing(t *testing.T) {
	_, guard, profile := fixture(t)
	tree := filepath.Join(profile, ".agents", "skills", "old")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, hashErr := artifact.TreeHash(tree, placement.Windows)
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	receipts := t.TempDir()
	if err := receipt.Write(receipts, receipt.Receipt{Item: "old", Placement: "codex", Path: tree, TreeHash: hash, InstalledAtUTC: time.Now()}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("service unavailable")
	if _, err := Sync(context.Background(), &fakeClient{err: wantErr}, guard, receipts, slog.Default()); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(filepath.Join(tree, "SKILL.md")); err != nil {
		t.Errorf("tree removed without a plan: %v", err)
	}

	// With an empty plan the same receipt is out of scope and is removed,
	// under the user roots.
	results, err := Sync(context.Background(), &fakeClient{plan: []service.UserPlacement{}}, guard, receipts, slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeRemoved) {
		t.Errorf("results = %+v, %v; want old removed", results, err)
	}
}

// The data and binary directories stay refused in user scope, even inside a
// user root.
func TestSyncRefusesDataDir(t *testing.T) {
	entry, guard, profile := fixture(t)
	guard.DataDir = filepath.Join(profile, ".agents")
	results, err := Sync(context.Background(), &fakeClient{plan: []service.UserPlacement{entry}}, guard, t.TempDir(), slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeRefused) {
		t.Errorf("results = %+v, %v; want refused", results, err)
	}
}

// fakeWait records the waits Sync asks for instead of sleeping.
func fakeWait(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	orig := wait
	wait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	t.Cleanup(func() { wait = orig })
	return &waits
}

// At logon the service may still be starting: a connection failure is
// retried every five seconds, and the plan that then arrives is applied.
func TestSyncRetriesUnreachableService(t *testing.T) {
	entry, guard, profile := fixture(t)
	waits := fakeWait(t)
	refused := fmt.Errorf("%w pipe: connection refused", service.ErrConnect)
	client := &fakeClient{plan: []service.UserPlacement{entry}, failures: []error{refused, refused}}
	results, err := Sync(context.Background(), client, guard, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 3 || !slices.Equal(*waits, []time.Duration{5 * time.Second, 5 * time.Second}) {
		t.Errorf("calls %d, waits %v; want 3 calls, two 5s waits", client.calls, *waits)
	}
	if len(results) != 1 || results[0].Outcome != string(installer.OutcomeInstalled) {
		t.Errorf("results = %+v, want skill installed", results)
	}
	if _, err := os.Stat(filepath.Join(profile, ".agents", "skills", "skill", "SKILL.md")); err != nil {
		t.Errorf("skill not placed: %v", err)
	}
}

// Retries stop after a minute, and an error the service answered is not
// retried at all; either way nothing is removed.
func TestSyncRetryLimits(t *testing.T) {
	_, guard, _ := fixture(t)
	waits := fakeWait(t)
	refused := fmt.Errorf("%w pipe: connection refused", service.ErrConnect)
	client := &fakeClient{err: refused}
	if _, err := Sync(context.Background(), client, guard, t.TempDir(), slog.Default()); !errors.Is(err, service.ErrConnect) {
		t.Errorf("err = %v, want ErrConnect", err)
	}
	if client.calls != 13 || len(*waits) != 12 {
		t.Errorf("calls %d, waits %d; want 13 calls over 60s", client.calls, len(*waits))
	}

	*waits = nil
	answered := &fakeClient{err: &service.Error{Message: "command failed"}}
	if _, err := Sync(context.Background(), answered, guard, t.TempDir(), slog.Default()); err == nil || answered.calls != 1 || len(*waits) != 0 {
		t.Errorf("answered error: err %v, calls %d, waits %d; want one call, no retry", err, answered.calls, len(*waits))
	}
}

// The helper's receipt carries the plan's managed flag and its templates
// render with the plan's facts and the account's login name, without domain;
// a managed flip in the plan re-renders in place, as in machine scope.
func TestSyncRendersTemplates(t *testing.T) {
	orig := currentUser
	currentUser = func() (*user.User, error) { return &user.User{Username: `CORP\alice`}, nil }
	t.Cleanup(func() { currentUser = orig })

	entry, guard, profile := fixtureTree(t, map[string]string{
		"SKILL.md":     "skill",
		"NOTE.md.tmpl": `{{ .User }} managed={{ .Item.Managed }} by {{ fact "assigned_user" }} on {{ .OS }}`,
	})
	entry.Managed = true
	client := &fakeClient{plan: []service.UserPlacement{entry}, facts: conditions.Facts{"assigned_user": "jdoe", "ipv4_address": []string{"10.0.0.1"}}}
	receipts := filepath.Join(t.TempDir(), "receipts")
	results, err := Sync(context.Background(), client, guard, receipts, slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeInstalled) {
		t.Fatalf("results = %+v, %v; want installed", results, err)
	}
	note := filepath.Join(profile, ".agents", "skills", "skill", "NOTE.md")
	if got, readErr := os.ReadFile(note); readErr != nil || string(got) != "alice managed=true by jdoe on windows" {
		t.Fatalf("NOTE.md = %q, %v", got, readErr)
	}
	rc, ok, err := receipt.Read(receipts, "codex", "skill")
	if err != nil || !ok || !rc.Managed || len(rc.Rendered) != 1 || rc.PlacedHash == rc.TreeHash {
		t.Fatalf("receipt = %+v, %v, %v", rc, ok, err)
	}
	var inputs struct {
		User  string                 `json:"user"`
		Scope string                 `json:"scope"`
		Item  struct{ Managed bool } `json:"item"`
		Facts conditions.Facts       `json:"facts"`
	}
	if err := json.Unmarshal(rc.Rendered[0].Inputs, &inputs); err != nil {
		t.Fatal(err)
	}
	if inputs.User != "alice" || inputs.Scope != "user" || !inputs.Item.Managed || len(inputs.Facts) != 1 || inputs.Facts["assigned_user"] != "jdoe" {
		t.Fatalf("inputs = %+v", inputs)
	}

	// Nothing changed: the receipt is left alone.
	if results, _ := Sync(context.Background(), client, guard, receipts, slog.Default()); results[0].Outcome != string(installer.OutcomeCurrent) {
		t.Fatalf("second sync = %+v, want current", results)
	}

	// The manifest moves the item out of managed_installs: re-rendered in
	// place from the archive-free tree, receipt flipped.
	client.plan[0].Managed = false
	if results, _ := Sync(context.Background(), client, guard, receipts, slog.Default()); results[0].Outcome != string(installer.OutcomeAdopted) {
		t.Fatalf("flip sync = %+v, want adopted", results)
	}
	if got, _ := os.ReadFile(note); string(got) != "alice managed=false by jdoe on windows" {
		t.Fatalf("NOTE.md after the flip = %q", got)
	}
	if rc, _, _ := receipt.Read(receipts, "codex", "skill"); rc.Managed {
		t.Fatal("receipt still managed after the flip")
	}
}

// loginName strips a Windows domain prefix and a UPN suffix.
func TestLoginName(t *testing.T) {
	orig := currentUser
	t.Cleanup(func() { currentUser = orig })
	for username, want := range map[string]string{`CORP\alice`: "alice", "alice@corp.example.com": "alice", "alice": "alice", `CORP\alice@x`: "alice"} {
		currentUser = func() (*user.User, error) { return &user.User{Username: username}, nil }
		if got, err := loginName(); got != want || err != nil {
			t.Errorf("loginName(%q) = %q, %v; want %q", username, got, err, want)
		}
	}

	// Without a user the sync fails before placing anything, rather than
	// recording user: "" in the receipts; the next logon retries.
	noUser := errors.New("no user")
	currentUser = func() (*user.User, error) { return nil, noUser }
	entry, guard, profile := fixture(t)
	client := &fakeClient{plan: []service.UserPlacement{entry}}
	results, err := Sync(context.Background(), client, guard, filepath.Join(t.TempDir(), "receipts"), slog.Default())
	if !errors.Is(err, noUser) || len(results) != 0 {
		t.Fatalf("Sync without a user = %+v, %v; want the cause", results, err)
	}
	if _, err := os.Stat(filepath.Join(profile, ".agents")); !os.IsNotExist(err) {
		t.Errorf("something was written under the profile: %v", err)
	}
	if len(client.reported) != 0 {
		t.Errorf("reported %+v without a user", client.reported)
	}
}
