package main

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/filelock"
	"github.com/hurricanehrndz/sofcat/pkg/manifest"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
	"github.com/hurricanehrndz/sofcat/pkg/status"
)

// lockedRun is a run against a repository whose manifest lists nothing, so
// the run succeeds. The test holds the run lock on the run's data directory,
// as another run would, until release.
type lockedRun struct {
	cfg      config.Configuration
	unlock   func()
	released atomic.Bool
	// inWork, when set, runs once as the run fetches its manifest: the run
	// is then past the lock and inside its work.
	inWork func()
	once   sync.Once
}

func newLockedRun(t *testing.T) *lockedRun {
	t.Helper()
	lr := &lockedRun{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/catalogs/lock_catalog.yaml") {
			_, _ = w.Write([]byte("{}\n"))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/manifests/lock_manifest.yaml") {
			http.NotFound(w, r)
			return
		}
		if !lr.released.Load() {
			t.Error("the run fetched its manifest while another run held the lock")
		}
		if lr.inWork != nil {
			lr.once.Do(lr.inWork)
		}
		_, _ = w.Write([]byte("name: lock_manifest\n"))
	}))
	t.Cleanup(srv.Close)
	adminCheckFunc = func() (bool, error) { return true, nil }
	lr.cfg = config.Configuration{
		CachePath:   t.TempDir(),
		AppDataPath: t.TempDir(),
		URL:         srv.URL + "/",
		URLPackages: srv.URL + "/",
		Manifest:    "lock_manifest",
		Catalogs:    []string{"lock_catalog"},
	}
	unlock, err := filelock.Lock(lr.lockPath())
	if err != nil {
		t.Fatal(err)
	}
	lr.unlock = unlock
	return lr
}

func (lr *lockedRun) lockPath() string { return filepath.Join(lr.cfg.AppDataPath, runLockName) }

func (lr *lockedRun) release() {
	lr.released.Store(true)
	lr.unlock()
}

// assertWaits fails unless done stays open while the test holds the lock,
// then releases it and waits for done.
func (lr *lockedRun) assertWaits(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("the run did not wait for the lock")
	case <-time.After(200 * time.Millisecond):
	}
	lr.release()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not proceed after the lock was released")
	}
}

// TestCommandLineRunRefusesWhileLocked: a command-line run beside another run
// must not touch the shared state, so it fails at once and writes nothing.
func TestCommandLineRunRefusesWhileLocked(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	defer sofcatlog.Close()
	lr := newLockedRun(t)

	if _, err := managedRun(lr.cfg, nil, nil); !errors.Is(err, errRunInProgress) {
		t.Fatalf("managedRun while locked: err = %v, want errRunInProgress", err)
	}
	entries, err := os.ReadDir(lr.cfg.AppDataPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != runLockName {
			t.Errorf("refused run wrote %s", e.Name())
		}
	}

	lr.release()
	if _, err := managedRun(lr.cfg, nil, nil); err != nil {
		t.Fatalf("managedRun after release: %v", err)
	}
	readInventory(t, lr.cfg.AppDataPath)
}

// TestServiceRunWaitsForLock: the service's run queues behind a command-line
// run rather than failing, goes ahead once the lock is free, and holds the
// lock itself through its work, releasing it only when it returns.
func TestServiceRunWaitsForLock(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	defer sofcatlog.Close()
	lr := newLockedRun(t)
	lr.cfg.ServiceMode = true
	lr.inWork = func() {
		if _, err := filelock.TryLock(lr.lockPath()); !errors.Is(err, filelock.ErrLocked) {
			t.Errorf("TryLock during the run: err = %v, want ErrLocked", err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := managedRun(lr.cfg, nil, nil); err != nil {
			t.Errorf("managedRun: %v", err)
		}
	}()
	lr.assertWaits(t, done)
	readInventory(t, lr.cfg.AppDataPath)

	unlock, err := filelock.TryLock(lr.lockPath())
	if err != nil {
		t.Fatalf("TryLock after the run: %v", err)
	}
	unlock()
}

// TestPruneChecksDoNotHoldSelfServe: the prune's status checks may run slow
// scripts, so the service's mutations and lists must not wait for them, and a
// selection made meanwhile must survive the prune's save.
func TestPruneChecksDoNotHoldSelfServe(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	defer sofcatlog.Close()
	lr := newLockedRun(t)
	lr.release()
	path := manifest.SelfServePath(lr.cfg.AppDataPath)
	if err := manifest.SaveSelfServe(path, manifest.Item{
		Name:       "service-manifest",
		Uninstalls: []string{"Reselected", "Done"},
		SelectedBy: map[string]string{"Reselected": "old", "Done": "old"},
	}); err != nil {
		t.Fatal(err)
	}

	goneUninstalls = func(names []string, _ map[int]map[string]catalog.Item, _ *status.Checker, _ string) []string {
		// A user selects Reselected for install while the checks run, as the
		// service's setSelection does.
		mutated := make(chan error, 1)
		go func() {
			mutated <- manifest.UpdateSelfServe(path, func(entry *manifest.Item) bool {
				entry.Uninstalls = slices.DeleteFunc(entry.Uninstalls, func(n string) bool { return n == "Reselected" })
				entry.Installs = append(entry.Installs, "Reselected")
				entry.SelectedBy["Reselected"] = "new"
				return true
			})
		}()
		select {
		case err := <-mutated:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("a mutation waited for the prune's status checks")
		}
		return names
	}

	if _, err := managedRun(lr.cfg, nil, nil); err != nil {
		t.Fatalf("managedRun: %v", err)
	}
	got, err := manifest.LoadSelfServe(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Uninstalls) != 0 || !slices.Contains(got.Installs, "Reselected") {
		t.Errorf("after prune: installs %v, uninstalls %v; want Reselected kept for install and Done pruned", got.Installs, got.Uninstalls)
	}
	if want := map[string]string{"Reselected": "new"}; !maps.Equal(got.SelectedBy, want) {
		t.Errorf("selected_by after prune = %v, want %v", got.SelectedBy, want)
	}
}

// TestCheckOnlyRunWaitsForLock: a check-only run, which monitoring starts on
// its own schedule, waits for another run instead of failing, then prints its
// inventory as usual.
func TestCheckOnlyRunWaitsForLock(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	defer sofcatlog.Close()
	lr := newLockedRun(t)
	lr.cfg.CheckOnly = true

	done := make(chan struct{})
	var stdout string
	go func() {
		defer close(done)
		stdout = captureStdout(t, func() {
			if err := route(lr.cfg); err != nil {
				t.Errorf("route: %v", err)
			}
		})
	}()
	lr.assertWaits(t, done)
	var inv report.Inventory
	if err := json.Unmarshal([]byte(stdout), &inv); err != nil {
		t.Fatalf("stdout is not the inventory JSON: %v\n%s", err, stdout)
	}
	if inv.ManifestName != "lock_manifest" {
		t.Errorf("ManifestName = %q", inv.ManifestName)
	}
}
