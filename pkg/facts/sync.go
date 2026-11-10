package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

// SyncedDir is where Sync keeps the repository's condition scripts, apart
// from the admin's own directory (Dir), which SofCat never writes.
func SyncedDir(appDataPath string) string {
	return filepath.Join(appDataPath, "conditions-synced")
}

// Dirs are the directories Collect runs scripts from, in order: the synced
// scripts first, so a script the admin installed by hand wins a shared fact.
func Dirs(appDataPath string) []string {
	return []string{SyncedDir(appDataPath), Dir(appDataPath)}
}

// Sync makes dir hold exactly the scripts the repository's index lists for
// this OS. fetch returns a script's bytes by its path in the repository. A
// script whose copy already has the listed hash is not fetched. One that
// cannot be fetched, or whose bytes do not have the listed hash, is logged
// and keeps its previous copy, so an unreachable repository does not take
// facts away. Every file the index does not list is deleted.
func Sync(dir string, scripts []conditions.Script, fetch func(repoPath string) ([]byte, error)) {
	syncScripts(dir, runtime.GOOS, scripts, fetch)
}

func syncScripts(dir, goos string, scripts []conditions.Script, fetch func(string) ([]byte, error)) {
	// want maps a file name in dir to its index entry. folded holds the names
	// lowercased: macOS and Windows would store two that differ by case as one.
	want := make(map[string]conditions.Script)
	folded := make(map[string]bool)
	for _, script := range scripts {
		if script.OS != goos {
			continue
		}
		name, err := syncedName(script, goos)
		if err == nil && folded[strings.ToLower(name)] {
			err = errors.New("another script has this name")
		}
		if err != nil {
			slog.Warn("ignoring a condition script in the repository's index", "path", script.Path, "err", err)
			continue
		}
		want[name] = script
		folded[strings.ToLower(name)] = true
	}

	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		slog.Warn("unable to read the synced conditions directory", "path", dir, "err", err)
		return
	}
	for _, entry := range entries {
		if _, ok := want[entry.Name()]; ok {
			continue
		}
		stale := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(stale); err != nil {
			slog.Warn("unable to remove a condition script the repository dropped", "path", stale, "err", err)
			continue
		}
		slog.Info("removed a condition script the repository dropped", "path", stale)
	}
	if len(want) == 0 {
		return
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("unable to create the synced conditions directory", "path", dir, "err", err)
		return
	}
	for name, script := range want {
		if err := syncScript(filepath.Join(dir, name), script, fetch); err != nil {
			slog.Warn("unable to sync a condition script; keeping any earlier copy", "path", script.Path, "err", err)
		}
	}
}

// syncScript brings the copy at dst up to the index entry: nothing to fetch
// when its bytes already have the listed hash, otherwise fetch, verify, write.
func syncScript(dst string, script conditions.Script, fetch func(string) ([]byte, error)) error {
	if have, err := os.ReadFile(dst); err == nil && hashMatches(have, script.SHA256) {
		// A copy with the right bytes may still have lost its mode.
		return os.Chmod(dst, syncedMode)
	}
	body, err := fetch(script.Path)
	if err != nil {
		return err
	}
	if !hashMatches(body, script.SHA256) {
		return errors.New("its sha256 is not the one the index lists")
	}
	if err := writeScript(dst, body); err != nil {
		return err
	}
	slog.Info("synced a condition script", "path", dst)
	return nil
}

// syncedMode is what the macOS script rule (scriptEligible) needs: executable
// and writable by the owner only. The service runs as root, so it owns the file.
const syncedMode = 0o755

// syncedName returns the file name an index entry is stored under. The index
// comes from the network, so only the exact shape makecatalogs writes is
// accepted: conditions/<goos>/<name>, where name is a single path element
// that cannot leave dir, hide as a dot-file or name an NTFS stream.
func syncedName(script conditions.Script, goos string) (string, error) {
	name := path.Base(script.Path)
	if script.Path != path.Join("conditions", goos, name) {
		return "", fmt.Errorf("not of the form conditions/%s/<name>", goos)
	}
	if strings.HasPrefix(name, ".") || strings.ContainsAny(name, `\:`) {
		return "", fmt.Errorf("unusable file name %q", name)
	}
	return name, nil
}

func hashMatches(body []byte, want string) bool {
	sum := sha256.Sum256(body)
	return strings.EqualFold(hex.EncodeToString(sum[:]), want)
}

// writeScript writes body to path through a temporary file renamed into
// place, so a run never executes a partial script.
func writeScript(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sync-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(body)
	if err == nil {
		err = tmp.Chmod(syncedMode)
	}
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
