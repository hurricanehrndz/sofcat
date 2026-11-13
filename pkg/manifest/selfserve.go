package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/hurricanehrndz/sofcat/pkg/filelock"
	"go.yaml.in/yaml/v4"
)

// mkdirAll is a test seam for the self-serve save path.
var mkdirAll = os.MkdirAll

// SelfServePath returns the path to the self-serve manifest under appDataPath.
func SelfServePath(appDataPath string) string {
	return filepath.Join(appDataPath, "service-manifest.yaml")
}

// loadSelfServe reads the self-serve manifest at path without the lock.
func loadSelfServe(path string) (Item, error) {
	defaultManifest := Item{
		Name:     "service-manifest",
		Installs: []string{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultManifest, nil
		}
		return Item{}, fmt.Errorf("unable to read service local manifest %s: %w", path, err)
	}

	entry := defaultManifest
	if err := yaml.Unmarshal(data, &entry); err != nil {
		return Item{}, fmt.Errorf("unable to parse service local manifest %s: %w", path, err)
	}
	if entry.Name == "" {
		entry.Name = defaultManifest.Name
	}
	return entry, nil
}

// SaveSelfServe persists the self-serve manifest at path. It keeps the three
// self-serve lists (managed_installs, managed_uninstalls, default_installs) and
// the selected_by record, and drops includes/catalogs/updates/conditional_items,
// which the self-serve file never owns. It takes no lock: callers go through
// UpdateSelfServe.
func SaveSelfServe(path string, entry Item) error {
	if err := mkdirAll(filepath.Clean(filepath.Dir(path)), 0o755); err != nil {
		return fmt.Errorf("unable to create local manifest directory: %w", err)
	}

	entry.Includes = nil
	entry.Catalogs = nil
	entry.Updates = nil
	entry.ConditionalItems = nil

	data, err := yaml.Marshal(entry)
	if err != nil {
		return fmt.Errorf("unable to encode service local manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("unable to write service local manifest %s: %w", path, err)
	}
	return nil
}

// selfServeMu serialises access to the self-serve manifest within a process.
// The service writes it from its command queue and, for a cancel, while a
// managed run in the same process is under way, so a plain load and save could
// lose a write.
var selfServeMu sync.Mutex

// lockSelfServe takes selfServeMu and the file lock path+".lock", which a
// command-line run beside the service needs: its saves would otherwise race
// the service's mutations and reads. Every hold is one load, or one load,
// change and save, never a status check or a network call. It is taken inside
// the managed-run lock when a run calls this, and nothing under it takes that
// one, so the two never wait on each other.
func lockSelfServe(path string) (func(), error) {
	selfServeMu.Lock()
	unlock, err := filelock.Lock(path + ".lock")
	if err != nil {
		selfServeMu.Unlock()
		return nil, fmt.Errorf("unable to lock the self-serve manifest: %w", err)
	}
	return func() { unlock(); selfServeMu.Unlock() }, nil
}

// LoadSelfServe reads the self-serve manifest at path under its lock, so it
// never sees a save half written. A missing file returns a named empty
// manifest rather than an error. Never call it from an UpdateSelfServe
// callback, which already holds the lock.
func LoadSelfServe(path string) (Item, error) {
	unlock, err := lockSelfServe(path)
	if errors.Is(err, os.ErrNotExist) {
		// No directory for the lock file, so no manifest either.
		return loadSelfServe(path)
	}
	if err != nil {
		return Item{}, err
	}
	defer unlock()
	return loadSelfServe(path)
}

// UpdateSelfServe loads the self-serve manifest at path, lets fn change it, and
// saves it when fn reports a change, all under one hold of its lock. fn must
// be quick: every reader and writer of the file waits for it.
func UpdateSelfServe(path string, fn func(*Item) bool) error {
	if err := mkdirAll(filepath.Clean(filepath.Dir(path)), 0o755); err != nil {
		return fmt.Errorf("unable to create local manifest directory: %w", err)
	}
	unlock, err := lockSelfServe(path)
	if err != nil {
		return err
	}
	defer unlock()
	entry, err := loadSelfServe(path)
	if err != nil {
		return err
	}
	if !fn(&entry) {
		return nil
	}
	return SaveSelfServe(path, entry)
}
