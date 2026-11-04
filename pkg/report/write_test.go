package report

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteInventoryReplacesTarget: each run must fully replace the previous
// inventory and leave no temp files behind in ProgramData.
func TestWriteInventoryReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.json")
	if err := os.WriteFile(path, []byte("planted"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := WriteInventory(path, New().Inventory(Plan{ManifestName: "site_default"})); err != nil {
		t.Fatalf("WriteInventory: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Inventory
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatalf("target not replaced with inventory JSON: %v\n%s", err, data)
	}
	if got.ManifestName != "site_default" || got.SchemaVersion != SchemaVersion {
		t.Errorf("unexpected inventory: %#v", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only inventory.json in %s, found %d entries", dir, len(entries))
	}
}

// TestRenameRetrying: a transient failure is retried until the rename
// succeeds; any other failure, and exhaustion, return the last error.
func TestRenameRetrying(t *testing.T) {
	transient := errors.New("sharing violation")
	calls := 0
	rename := func(fails int) func(string, string) error {
		return func(string, string) error {
			calls++
			if calls <= fails {
				return transient
			}
			return nil
		}
	}
	retryable := func(err error) bool { return errors.Is(err, transient) }

	calls = 0
	if err := renameRetrying(rename(2), retryable, "a", "b"); err != nil || calls != 3 {
		t.Errorf("transient failures: err=%v calls=%d, want nil and 3", err, calls)
	}
	calls = 0
	if err := renameRetrying(rename(2), func(error) bool { return false }, "a", "b"); !errors.Is(err, transient) || calls != 1 {
		t.Errorf("non-retryable failure: err=%v calls=%d, want the error after 1 call", err, calls)
	}
	calls = 0
	if err := renameRetrying(rename(renameAttempts+5), retryable, "a", "b"); !errors.Is(err, transient) || calls != renameAttempts {
		t.Errorf("exhaustion: err=%v calls=%d, want the error after %d calls", err, calls, renameAttempts)
	}
}
