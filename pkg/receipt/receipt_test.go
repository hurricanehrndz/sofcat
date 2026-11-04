package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestRoundTrip: later phases and other processes read receipts from disk,
// so what Write saves must come back from Read and List unchanged, under the
// documented JSON field names.
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Receipt{
		Item: "hello", Version: "1.0.0", Placement: "skills",
		Path: "/roots/skills/hello", TreeHash: "sha256:abc", Managed: true,
		InstalledAtUTC: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
	}
	if err := Write(dir, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Read(dir, "skills", "hello")
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Read = %+v, %v, %v; want %+v", got, ok, err, want)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "skills", "hello.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"item", "version", "placement", "path", "tree_hash", "managed", "installed_at_utc"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("receipt JSON lacks %q: %s", key, raw)
		}
	}

	other := want
	other.Item, other.Placement = "aaa", "agents"
	if err = Write(dir, other); err != nil {
		t.Fatal(err)
	}
	list, err := List(dir)
	if err != nil || len(list) != 2 || list[0].Placement != "agents" || list[1].Item != "hello" {
		t.Fatalf("List = %+v, %v", list, err)
	}
	mine, err := ForItem(dir, "hello")
	if err != nil || len(mine) != 1 {
		t.Fatalf("ForItem = %+v, %v", mine, err)
	}

	if err := Delete(dir, "skills", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Read(dir, "skills", "hello"); ok {
		t.Fatal("receipt still there after Delete")
	}
	if err := Delete(dir, "skills", "hello"); err != nil {
		t.Fatalf("Delete of a missing receipt: %v", err)
	}
	if list, err := List(filepath.Join(dir, "missing")); err != nil || list != nil {
		t.Fatalf("List of a missing dir = %v, %v", list, err)
	}
}

// TestRejectsPathNames: names come from the repository and must not move a
// receipt outside its directory.
func TestRejectsPathNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if err := Write(t.TempDir(), Receipt{Item: name, Placement: "p"}); err == nil {
			t.Errorf("Write accepted item %q", name)
		}
	}
}
