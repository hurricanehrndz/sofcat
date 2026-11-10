package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

func indexEntry(name, body string) conditions.Script {
	sum := sha256.Sum256([]byte(body))
	return conditions.Script{Path: "conditions/darwin/" + name, OS: "darwin", SHA256: hex.EncodeToString(sum[:])}
}

// repoFetch serves bodies by repository path and records what was asked for.
func repoFetch(bodies map[string]string, fetched *[]string) func(string) ([]byte, error) {
	return func(repoPath string) ([]byte, error) {
		*fetched = append(*fetched, repoPath)
		body, ok := bodies[repoPath]
		if !ok {
			return nil, errors.New("unreachable")
		}
		return []byte(body), nil
	}
}

func dirFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(body)
	}
	return files
}

func wantFiles(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	got := dirFiles(t, dir)
	if len(got) != len(want) {
		t.Fatalf("synced files = %v, want %v", got, want)
	}
	for name, body := range want {
		if got[name] != body {
			t.Fatalf("synced files = %v, want %v", got, want)
		}
	}
}

func TestSyncScripts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conditions-synced")
	var fetched []string
	index := []conditions.Script{
		indexEntry("10-user", "user v1"),
		indexEntry("20-groups", "groups v1"),
		{Path: "conditions/windows/10-user.ps1", OS: "windows", SHA256: "00"},
	}
	repo := map[string]string{"conditions/darwin/10-user": "user v1", "conditions/darwin/20-groups": "groups v1"}

	// The first sync fetches this OS's scripts only and makes them runnable.
	syncScripts(dir, "darwin", index, repoFetch(repo, &fetched))
	wantFiles(t, dir, map[string]string{"10-user": "user v1", "20-groups": "groups v1"})
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "10-user"))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, %v; want 0755, which the macOS script rule needs", info.Mode(), err)
		}
	}

	// Unchanged scripts are not fetched again.
	fetched = nil
	syncScripts(dir, "darwin", index, repoFetch(repo, &fetched))
	if fetched != nil {
		t.Fatalf("an up-to-date sync fetched %v", fetched)
	}

	// A new version replaces the copy; a script the index dropped is deleted.
	index = []conditions.Script{indexEntry("10-user", "user v2")}
	repo = map[string]string{"conditions/darwin/10-user": "user v2"}
	syncScripts(dir, "darwin", index, repoFetch(repo, &fetched))
	wantFiles(t, dir, map[string]string{"10-user": "user v2"})

	// An empty index, which is also what a repository without one has,
	// removes the rest.
	syncScripts(dir, "darwin", nil, repoFetch(nil, &fetched))
	wantFiles(t, dir, map[string]string{})
}

// A repository that is unreachable, or serves bytes the index does not vouch
// for, must not take a working script away or put an unverified one in place.
func TestSyncKeepsEarlierCopyOnFailure(t *testing.T) {
	dir := t.TempDir()
	var fetched []string
	v1 := []conditions.Script{indexEntry("10-user", "user v1")}
	syncScripts(dir, "darwin", v1, repoFetch(map[string]string{"conditions/darwin/10-user": "user v1"}, &fetched))

	v2 := []conditions.Script{indexEntry("10-user", "user v2"), indexEntry("20-new", "new")}
	tests := map[string]map[string]string{
		"fetch fails":   {},
		"hash mismatch": {"conditions/darwin/10-user": "tampered", "conditions/darwin/20-new": "tampered"},
	}
	for name, repo := range tests {
		t.Run(name, func(t *testing.T) {
			syncScripts(dir, "darwin", v2, repoFetch(repo, &fetched))
			wantFiles(t, dir, map[string]string{"10-user": "user v1"})
		})
	}
}

// The index comes from the network: an entry must not write outside the
// synced directory or under a name the index's own layout would not produce.
func TestSyncRejectsUnsafeIndexEntries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "conditions-synced")
	sum := sha256.Sum256([]byte("x"))
	hash := hex.EncodeToString(sum[:])
	var index []conditions.Script
	for _, p := range []string{
		"conditions/darwin/../../evil",
		"../evil",
		"/etc/evil",
		"conditions/darwin/sub/evil",
		"conditions/windows/evil",
		"conditions/darwin/.hidden",
		`conditions/darwin/..\evil`,
		"conditions/darwin/evil:stream",
		"conditions/darwin/",
		"conditions/darwin",
	} {
		index = append(index, conditions.Script{Path: p, OS: "darwin", SHA256: hash})
	}
	// Two names one file system entry apart: only the first is kept.
	index = append(index,
		conditions.Script{Path: "conditions/darwin/ok", OS: "darwin", SHA256: hash},
		conditions.Script{Path: "conditions/darwin/OK", OS: "darwin", SHA256: hash},
	)

	var fetched []string
	syncScripts(dir, "darwin", index, func(repoPath string) ([]byte, error) {
		fetched = append(fetched, repoPath)
		return []byte("x"), nil
	})
	if !slices.Equal(fetched, []string{"conditions/darwin/ok"}) {
		t.Fatalf("fetched %v, want only conditions/darwin/ok", fetched)
	}
	wantFiles(t, dir, map[string]string{"ok": "x"})
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("wrote outside the synced directory: %v", entries)
	}
}

// A script installed by hand runs after the synced ones, so the admin's copy
// wins a fact both set.
func TestCollectRunsSyncedScriptsFirst(t *testing.T) {
	appData := t.TempDir()
	for _, dir := range Dirs(appData) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "10-site.ps1"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	restore := stub(t, func(path string) ([]byte, error) {
		if filepath.Dir(path) == SyncedDir(appData) {
			return []byte(`{"site": "synced", "team": "synced"}`), nil
		}
		return []byte(`{"site": "admin"}`), nil
	}, func() conditions.Facts { return conditions.Facts{} })
	defer restore()

	facts := Collect(Dirs(appData), true)
	if facts["site"] != "admin" || facts["team"] != "synced" {
		t.Fatalf("facts = %v; want site from the admin script and team from the synced one", facts)
	}
}

// A Windows device takes the Windows scripts and none of the Mac ones.
func TestSyncScriptsWindows(t *testing.T) {
	dir := t.TempDir()
	sum := sha256.Sum256([]byte("win"))
	index := []conditions.Script{
		indexEntry("10-user", "mac"),
		{Path: "conditions/windows/10-user.ps1", OS: "windows", SHA256: hex.EncodeToString(sum[:])},
	}
	var fetched []string
	syncScripts(dir, "windows", index, repoFetch(map[string]string{"conditions/windows/10-user.ps1": "win"}, &fetched))
	wantFiles(t, dir, map[string]string{"10-user.ps1": "win"})
}
