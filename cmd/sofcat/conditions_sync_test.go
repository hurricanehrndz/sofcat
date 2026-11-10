package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/config"
	"github.com/hurricanehrndz/sofcat/pkg/facts"
	"github.com/hurricanehrndz/sofcat/pkg/sofcatlog"
)

// TestManagedRunSyncsConditionScripts drives the run against a repository
// that delivers a condition script: the run stores it, keeps it while the
// index cannot be fetched, and removes it once the repository has no index.
func TestManagedRunSyncsConditionScripts(t *testing.T) {
	resetMainHooks()
	defer resetMainHooks()
	defer sofcatlog.Close() // before t.TempDir is removed; see TestManagedRunReconcilesSelfServe

	const script = "'{}'\n"
	sum := sha256.Sum256([]byte(script))
	scriptPath := "conditions/" + runtime.GOOS + "/10-run-test.ps1"
	index := fmt.Sprintf("- path: %s\n  os: %s\n  sha256: %s\n", scriptPath, runtime.GOOS, hex.EncodeToString(sum[:]))

	indexStatus := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/sync_manifest.yaml"):
			_, _ = w.Write([]byte("name: sync_manifest\ncatalogs:\n  - sync_catalog\n"))
		case strings.HasSuffix(r.URL.Path, "/catalogs/sync_catalog.yaml"):
			_, _ = w.Write([]byte("Demo:\n  display_name: Demo\n"))
		case strings.HasSuffix(r.URL.Path, "/catalogs/conditions.yaml") && indexStatus == http.StatusOK:
			_, _ = w.Write([]byte(index))
		case strings.HasSuffix(r.URL.Path, "/catalogs/conditions.yaml"):
			w.WriteHeader(indexStatus)
		case strings.HasSuffix(r.URL.Path, "/"+scriptPath):
			_, _ = w.Write([]byte(script))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	appData := t.TempDir()
	cfg := config.Configuration{
		CheckOnly:   true,
		CachePath:   t.TempDir(),
		AppDataPath: appData,
		URL:         srv.URL + "/",
		URLPackages: srv.URL + "/",
		Manifest:    "sync_manifest",
		Catalogs:    []string{"sync_catalog"},
	}
	adminCheckFunc = func() (bool, error) { return true, nil }
	synced := filepath.Join(facts.SyncedDir(appData), "10-run-test.ps1")

	steps := []struct {
		name       string
		status     int
		wantSynced bool
	}{
		{"index lists the script", http.StatusOK, true},
		{"index unreachable", http.StatusInternalServerError, true},
		{"repository has no index", http.StatusNotFound, false},
	}
	for _, step := range steps {
		indexStatus = step.status
		if _, err := managedRun(cfg, nil, nil); err != nil {
			t.Fatalf("%s: managedRun failed: %v", step.name, err)
		}
		body, err := os.ReadFile(synced)
		if step.wantSynced && (err != nil || string(body) != script) {
			t.Fatalf("%s: synced script = %q, %v", step.name, body, err)
		}
		if !step.wantSynced && !os.IsNotExist(err) {
			t.Fatalf("%s: synced script still there (%v)", step.name, err)
		}
	}
}
