package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/filelock"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
)

// lockedRunConfig is a real (not check-only) run against a repository with
// nothing in it, so the run fails at the manifest but still writes the log and
// inventory.json, the files that show it got past the lock. The returned
// function releases the lock it holds on the run's data directory, as another
// run would.
func lockedRunConfig(t *testing.T) (config.Configuration, func()) {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	adminCheckFunc = func() (bool, error) { return true, nil }
	cfg := config.Configuration{
		CachePath:   t.TempDir(),
		AppDataPath: t.TempDir(),
		URL:         srv.URL + "/",
		URLPackages: srv.URL + "/",
		Manifest:    "lock_manifest",
	}
	unlock, err := filelock.Lock(filepath.Join(cfg.AppDataPath, runLockName))
	if err != nil {
		t.Fatal(err)
	}
	return cfg, unlock
}

// TestCommandLineRunRefusesWhileLocked: a command-line run beside another run
// must not touch the shared state, so it fails at once and writes nothing.
func TestCommandLineRunRefusesWhileLocked(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	defer sofcatlog.Close()
	cfg, unlock := lockedRunConfig(t)

	if _, err := managedRun(cfg, nil, nil); !errors.Is(err, errRunInProgress) {
		t.Fatalf("managedRun while locked: err = %v, want errRunInProgress", err)
	}
	entries, err := os.ReadDir(cfg.AppDataPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != runLockName {
			t.Errorf("refused run wrote %s", e.Name())
		}
	}

	unlock()
	_, _ = managedRun(cfg, nil, nil)
	readInventory(t, cfg.AppDataPath)
}

// TestServiceRunWaitsForLock: the service's run queues behind a command-line
// run rather than failing, and goes ahead once the lock is free.
func TestServiceRunWaitsForLock(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	defer sofcatlog.Close()
	cfg, unlock := lockedRunConfig(t)
	cfg.ServiceMode = true

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = managedRun(cfg, nil, nil)
	}()
	select {
	case <-done:
		t.Fatal("service run did not wait for the lock")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(cfg.AppDataPath, inventoryFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inventory written while waiting for the lock: %v", err)
	}

	unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("service run did not proceed after the lock was released")
	}
	readInventory(t, cfg.AppDataPath)
}
