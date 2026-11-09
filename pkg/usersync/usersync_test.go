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
	// placements are the plan's specs, passed through JSON as over the pipe;
	// nil is a plan from an older service.
	placements map[string]placement.Spec
	err        error
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
	plan := service.UserPlacements{Entries: c.plan, Facts: c.facts}
	if c.placements != nil {
		data, err := json.Marshal(c.placements)
		if err != nil {
			return service.UserPlacements{}, err
		}
		if err := json.Unmarshal(data, &plan.Placements); err != nil {
			return service.UserPlacements{}, err
		}
	}
	return plan, c.err
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
// retried every five seconds, and the plan that then arrives is applied
// (fetched once more under the lock, without retry).
func TestSyncRetriesUnreachableService(t *testing.T) {
	entry, guard, profile := fixture(t)
	waits := fakeWait(t)
	refused := fmt.Errorf("%w pipe: connection refused", service.ErrConnect)
	client := &fakeClient{plan: []service.UserPlacement{entry}, failures: []error{refused, refused}}
	results, err := Sync(context.Background(), client, guard, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 4 || !slices.Equal(*waits, []time.Duration{5 * time.Second, 5 * time.Second}) {
		t.Errorf("calls %d, waits %v; want 4 calls, two 5s waits", client.calls, *waits)
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

	// A helper the service started does not retry: one left waiting would
	// hold its executable open through an uninstall.
	defer func(d time.Duration) { PlanRetryFor = d }(PlanRetryFor)
	PlanRetryFor = 0
	unreachable := &fakeClient{err: refused}
	if _, err := Sync(context.Background(), unreachable, guard, t.TempDir(), slog.Default()); !errors.Is(err, service.ErrConnect) || unreachable.calls != 1 || len(*waits) != 0 {
		t.Errorf("no retry: err %v, calls %d, waits %d; want ErrConnect after one call", err, unreachable.calls, len(*waits))
	}
}

// The fetch under the lock is not retried: the service answered a moment
// before, and a sync holding the lock must not keep the user's other syncs
// waiting for a minute. Its error ends the sync with nothing removed.
func TestSyncSecondFetchFails(t *testing.T) {
	entry, guard, profile := fixture(t)
	receipts := t.TempDir()
	if _, err := Sync(context.Background(), &fakeClient{plan: []service.UserPlacement{entry}}, guard, receipts, slog.Default()); err != nil {
		t.Fatal(err)
	}
	waits := fakeWait(t)
	refused := fmt.Errorf("%w pipe: connection refused", service.ErrConnect)
	client := &fakeClient{failures: []error{nil, refused}}
	if _, err := Sync(context.Background(), client, guard, receipts, slog.Default()); !errors.Is(err, service.ErrConnect) || client.calls != 2 || len(*waits) != 0 {
		t.Errorf("err %v, calls %d, waits %d; want ErrConnect after two calls, no retry", err, client.calls, len(*waits))
	}
	if _, err := os.Stat(filepath.Join(profile, ".agents", "skills", "skill", "SKILL.md")); err != nil {
		t.Errorf("skill removed after the second fetch failed: %v", err)
	}
}

// signalClient reports each plan fetch on fetched. After the first fetch the
// plan becomes next, as if the service saved a new one meanwhile.
type signalClient struct {
	*fakeClient
	fetched chan struct{}
	next    []service.UserPlacement
}

func (c signalClient) ListUserPlacements(ctx context.Context) (service.UserPlacements, error) {
	c.fetched <- struct{}{}
	plan, err := c.fakeClient.ListUserPlacements(ctx)
	c.plan = c.next
	return plan, err
}

// One user's syncs overlap at logon, on a service start and across two
// sessions. A second sync fetches the plan once before the lock, so it exits
// at once when no service answers, then waits for the first's lock and
// fetches again, so it applies the newest plan rather than the one it saw
// before waiting: the first fetch here is empty, and only the second carries
// the entry.
func TestSyncWaitsForLock(t *testing.T) {
	entry, guard, profile := fixture(t)
	receipts := t.TempDir()
	unlock, err := lock(receipts)
	if err != nil {
		t.Fatal(err)
	}
	client := signalClient{&fakeClient{}, make(chan struct{}, 1), []service.UserPlacement{entry}}
	done := make(chan error, 1)
	go func() {
		_, err := Sync(context.Background(), client, guard, receipts, slog.Default())
		done <- err
	}()
	select {
	case <-client.fetched:
	case <-time.After(5 * time.Second):
		t.Fatal("the plan was not fetched before waiting for the lock")
	}
	select {
	case <-client.fetched:
		t.Fatal("the plan was fetched again while another sync held the lock")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	<-client.fetched
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(profile, ".agents", "skills", "skill", "SKILL.md")); err != nil {
		t.Errorf("skill not placed after the lock was released: %v", err)
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

// codexSpec is the spec of the fixture's placement as the plan carries it.
func codexSpec(entry service.UserPlacement) placement.Spec {
	return placement.Spec{
		Paths: map[string]string{placement.Windows: entry.Path},
		Scope: map[string]string{placement.Windows: placement.ScopeUser},
	}
}

// An exclusive user placement refuses a directory holding files it did not
// put there. A plan from an older service carries no spec, so the same
// directory takes the tree at <root>/<item>.
func TestSyncExclusive(t *testing.T) {
	entry, guard, profile := fixture(t)
	root := filepath.Join(profile, ".agents", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mine.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := codexSpec(entry)
	spec.Exclusive, spec.ItemsDir = true, "plugins"
	client := &fakeClient{plan: []service.UserPlacement{entry}, placements: map[string]placement.Spec{"codex": spec}}
	results, err := Sync(context.Background(), client, guard, t.TempDir(), slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeRefused) {
		t.Fatalf("results = %+v, %v; want refused", results, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "plugins")); !os.IsNotExist(statErr) {
		t.Errorf("exclusive placement written to: %v", statErr)
	}

	old := &fakeClient{plan: []service.UserPlacement{entry}}
	results, err = Sync(context.Background(), old, guard, t.TempDir(), slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeInstalled) {
		t.Fatalf("older plan: results = %+v, %v; want installed", results, err)
	}
	if _, err := os.Stat(filepath.Join(root, "skill", "SKILL.md")); err != nil {
		t.Errorf("older plan: skill not at <root>/<item>: %v", err)
	}
}

// A user placement's items_dir, vars and files apply as in machine scope:
// the tree lands under items_dir, the file renders with the user's root and
// login name, and it goes when the last item leaves the plan, which still
// carries the placement's spec.
func TestSyncPlacementSpec(t *testing.T) {
	orig := currentUser
	currentUser = func() (*user.User, error) { return &user.User{Username: `CORP\alice`}, nil }
	t.Cleanup(func() { currentUser = orig })

	entry, guard, profile := fixture(t)
	root := filepath.Join(profile, ".agents", "skills")
	spec := codexSpec(entry)
	spec.ItemsDir = "plugins"
	spec.Vars = map[string]any{"count": 2}
	spec.Files = []placement.File{{
		Path:     "index.txt",
		Template: `{{ range .Items }}{{ $.User }} {{ $.Placement.Root }} {{ .Name }} {{ .Path }} {{ $.Placement.Vars.count }}{{ end }}`,
	}}
	client := &fakeClient{plan: []service.UserPlacement{entry}, placements: map[string]placement.Spec{"codex": spec}}
	receipts := t.TempDir()
	results, err := Sync(context.Background(), client, guard, receipts, slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeInstalled) {
		t.Fatalf("results = %+v, %v; want installed", results, err)
	}
	tree := filepath.Join(root, "plugins", "skill")
	if _, statErr := os.Stat(filepath.Join(tree, "SKILL.md")); statErr != nil {
		t.Errorf("skill not under items_dir: %v", statErr)
	}
	index := filepath.Join(root, "index.txt")
	// The guard renders resolved paths; a Windows runner's TempDir can be an
	// 8.3 short name (RUNNER~1) that EvalSymlinks expands.
	resolved, evalErr := filepath.EvalSymlinks(root)
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	want := "alice " + resolved + " skill " + filepath.Join(resolved, "plugins", "skill") + " 2"
	if got, readErr := os.ReadFile(index); readErr != nil || string(got) != want {
		t.Errorf("index.txt = %q, %v", got, readErr)
	}

	client.plan = []service.UserPlacement{}
	results, err = Sync(context.Background(), client, guard, receipts, slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeRemoved) {
		t.Fatalf("results = %+v, %v; want removed", results, err)
	}
	if _, err := os.Stat(index); !os.IsNotExist(err) {
		t.Errorf("index.txt left after the last item: %v", err)
	}
}

// The helper's specs are user scope whatever the plan says: the guard holds
// the machine roots too, which a machine-scope spec would be checked against.
func TestUserTableForcesUserScope(t *testing.T) {
	spec := placement.Spec{Paths: map[string]string{placement.Windows: `C:\skills`}, Scope: map[string]string{placement.Windows: placement.ScopeMachine}}
	plan := service.UserPlacements{Placements: map[string]placement.Spec{"codex": spec}}
	table := userTable(plan, []receipt.Receipt{{Placement: "gone"}}, placement.Windows, slog.Default())
	if got := table.Placements["codex"].ScopeFor(placement.Windows); got != placement.ScopeUser {
		t.Errorf("codex scope = %s, want user", got)
	}
	if spec.Scope[placement.Windows] != placement.ScopeMachine {
		t.Error("userTable changed the plan's spec")
	}
	if !slices.Equal(table.Order, []string{"codex", "gone"}) {
		t.Errorf("order = %v", table.Order)
	}
}

// filesFixture installs the fixture's skill in a placement whose one file,
// .index.txt, lists its items, and returns the client, guard, receipt
// directory and the file's path.
func filesFixture(t *testing.T) (*fakeClient, placement.Guard, string, string) {
	t.Helper()
	entry, guard, profile := fixture(t)
	spec := codexSpec(entry)
	spec.Files = []placement.File{{Path: ".index.txt", Template: `{{ range .Items }}{{ .Name }}{{ end }}`}}
	client := &fakeClient{plan: []service.UserPlacement{entry}, placements: map[string]placement.Spec{"codex": spec}}
	receipts := t.TempDir()
	results, err := Sync(context.Background(), client, guard, receipts, slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeInstalled) {
		t.Fatalf("results = %+v, %v; want installed", results, err)
	}
	return client, guard, receipts, filepath.Join(profile, ".agents", "skills", ".index.txt")
}

// A deleted generated file comes back on the next sync, though no tree
// changed.
func TestSyncRestoresPlacementFile(t *testing.T) {
	client, guard, receipts, index := filesFixture(t)
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	results, err := Sync(context.Background(), client, guard, receipts, slog.Default())
	if err != nil || len(results) != 1 || results[0].Outcome != string(installer.OutcomeCurrent) {
		t.Fatalf("results = %+v, %v; want current", results, err)
	}
	if got, err := os.ReadFile(index); err != nil || string(got) != "skill" {
		t.Errorf("index.txt = %q, %v; want restored", got, err)
	}
}

// A file that fails to render is reported for its placement, with no item.
func TestSyncReportsPlacementFileFailure(t *testing.T) {
	client, guard, receipts, _ := filesFixture(t)
	spec := client.placements["codex"]
	spec.Files = []placement.File{{Path: ".index.txt", Template: `{{ fact "missing" }}`}}
	client.placements["codex"] = spec
	results, err := Sync(context.Background(), client, guard, receipts, slog.Default())
	if err != nil || len(results) != 2 || results[0].Outcome != string(installer.OutcomeCurrent) {
		t.Fatalf("results = %+v, %v; want current and a file failure", results, err)
	}
	if r := results[1]; r.Item != "" || r.Placement != "codex" || r.Outcome != string(installer.OutcomeRefused) || r.Error == "" {
		t.Errorf("file failure = %+v", r)
	}
	if !slices.Equal(client.reported, results) {
		t.Errorf("reported %+v, want %+v", client.reported, results)
	}
}

// A user-scope placement root can exist without anything of ours in it,
// such as a tool's profile directory: with no receipt of this user there,
// no file is generated.
func TestSyncNoPlacementFileWithoutReceipts(t *testing.T) {
	entry, guard, profile := fixture(t)
	root := filepath.Join(profile, ".agents", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := codexSpec(entry)
	spec.Files = []placement.File{{Path: ".index.txt", Template: `generated`}}
	client := &fakeClient{plan: []service.UserPlacement{}, placements: map[string]placement.Spec{"codex": spec}}
	results, err := Sync(context.Background(), client, guard, t.TempDir(), slog.Default())
	if err != nil || len(results) != 0 {
		t.Fatalf("results = %+v, %v; want none", results, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".index.txt")); !os.IsNotExist(err) {
		t.Errorf("index.txt generated without receipts: %v", err)
	}
}
