package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
	"go.yaml.in/yaml/v4"
)

func sha(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

const (
	macScript = "#!/bin/sh\necho '{\"assigned_user\": \"ana\"}'\n"
	winScript = "'{\"assigned_user\": \"ana\"}'\n"
)

// conditionsRepo is a repo with one condition script per OS.
func conditionsRepo(t *testing.T) string {
	t.Helper()
	repo := writeRepo(t, map[string]string{"chrome.yaml": chromeInfo})
	writeFiles(t, repo, map[string]string{
		"conditions/darwin/10-facts":      macScript,
		"conditions/windows/10-facts.ps1": winScript,
		"conditions/darwin/.DS_Store":     "ignored",
	})
	return repo
}

// The index is what a device trusts: it must list every script with the hash
// of the bytes the repository serves, and nothing else.
func TestWritesConditionsIndex(t *testing.T) {
	repo := conditionsRepo(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	body, err := os.ReadFile(filepath.Join(repo, "catalogs", "conditions.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var got []conditions.Script
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	want := []conditions.Script{
		{Path: "conditions/darwin/10-facts", OS: "darwin", SHA256: sha(macScript)},
		{Path: "conditions/windows/10-facts.ps1", OS: "windows", SHA256: sha(winScript)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("index = %+v, want %+v", got, want)
	}

	// A fresh index passes the check.
	stderr.Reset()
	if code := run([]string{"--check", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("--check after a build: exit %d, stderr: %s", code, stderr.String())
	}
}

// Dropping the last script must drop the index too, or devices would keep
// fetching scripts that are gone.
func TestNoScriptsNoIndex(t *testing.T) {
	repo := conditionsRepo(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if err := os.RemoveAll(filepath.Join(repo, "conditions")); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"--check", repo}, &stdout, &stderr); code != 1 {
		t.Fatalf("--check with a leftover index: exit %d, want 1", code)
	}
	if code := run([]string{repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "catalogs", "conditions.yaml")); !os.IsNotExist(err) {
		t.Fatalf("index still there: %v", err)
	}
}

func TestCheckConditions(t *testing.T) {
	tests := map[string]struct {
		// write and remove alter the repo after a build wrote its index.
		write   map[string]string
		remove  string
		wantErr string // "" passes the check
	}{
		"unchanged":     {},
		"script edited": {write: map[string]string{"conditions/darwin/10-facts": macScript + "# edited\n"}, wantErr: "stale"},
		"script added":  {write: map[string]string{"conditions/darwin/20-more": macScript}, wantErr: "stale"},
		"listed script missing": {
			remove:  "conditions/darwin/10-facts",
			wantErr: "lists conditions/darwin/10-facts, which is missing",
		},
		"hand-edited index": {write: map[string]string{"catalogs/conditions.yaml": "[]\n"}, wantErr: "stale"},
		// A repo that does not commit catalogs/ has nothing to be stale.
		"index never written": {remove: "catalogs"},
		"file outside the OS directories": {
			write:   map[string]string{"conditions/10-facts": macScript},
			wantErr: "belong in conditions/darwin or conditions/windows",
		},
		"unknown OS directory": {
			write:   map[string]string{"conditions/linux/10-facts": macScript},
			wantErr: "belong in conditions/darwin or conditions/windows",
		},
		"nested directory": {
			write:   map[string]string{"conditions/darwin/lib/helper": macScript},
			wantErr: "not a regular file",
		},
		"windows script type": {
			write:   map[string]string{"conditions/windows/10-facts.cmd": "echo {}\n"},
			wantErr: "only .ps1 and .exe",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			repo := conditionsRepo(t)
			var stdout, stderr bytes.Buffer
			if code := run([]string{repo}, &stdout, &stderr); code != 0 {
				t.Fatalf("build: exit %d, stderr: %s", code, stderr.String())
			}
			writeFiles(t, repo, tc.write)
			if tc.remove != "" {
				if err := os.RemoveAll(filepath.Join(repo, filepath.FromSlash(tc.remove))); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(t, repo)

			stderr.Reset()
			code := run([]string{"--check", repo}, &stdout, &stderr)
			if tc.wantErr == "" && code != 0 {
				t.Fatalf("exit %d, stderr: %s", code, stderr.String())
			}
			if tc.wantErr != "" && (code != 1 || !strings.Contains(stderr.String(), tc.wantErr)) {
				t.Fatalf("exit %d, stderr %q; want 1 and %q", code, stderr.String(), tc.wantErr)
			}
			if after := snapshot(t, repo); !reflect.DeepEqual(before, after) {
				t.Fatalf("--check modified the repo")
			}
		})
	}
}

// A catalog named conditions would be written over the index, and every agent
// would fetch it as one: refuse it even in a normal build.
func TestCatalogNamedConditionsIsRefused(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"chrome.yaml": chromeInfo,
		"other.yaml":  "item_name: Other\ncatalog: Conditions\n",
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "reserved") {
		t.Fatalf("exit %d, stderr %q; want 1 and a reserved-name error", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "catalogs")); !os.IsNotExist(err) {
		t.Fatalf("catalogs written: %v", err)
	}
}

// macOS and Windows devices store 10-Facts and 10-facts as one file, so the
// repository must not serve both.
func TestCheckConditionsCaseCollision(t *testing.T) {
	repo := conditionsRepo(t)
	writeFiles(t, repo, map[string]string{"conditions/darwin/10-Facts": macScript})
	if entries, _ := os.ReadDir(filepath.Join(repo, "conditions", "darwin")); len(entries) < 3 {
		t.Skip("case-insensitive file system")
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--check", repo}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "only by case") {
		t.Fatalf("exit %d, stderr %q; want 1 and a case collision", code, stderr.String())
	}
}
