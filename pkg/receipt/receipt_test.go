package receipt

import (
	"bytes"
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
		Path: "/roots/skills/hello", TreeHash: "sha256:abc", PlacedHash: "sha256:abc",
		Rendered: []Rendered{}, Managed: true,
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
	for _, key := range []string{"item", "version", "placement", "path", "tree_hash", "placed_hash", "rendered", "managed", "installed_at_utc"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("receipt JSON lacks %q: %s", key, raw)
		}
	}
	if rendered, ok := fields["rendered"].([]any); !ok || len(rendered) != 0 {
		t.Errorf("rendered = %v, want an empty list", fields["rendered"])
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

// TestLegacyReceipt: a receipt written before placed_hash and rendered
// existed records a tree placed as shipped, so Read answers as if it said so.
func TestLegacyReceipt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"item":"hello","version":"1.0.0","placement":"skills","path":"/roots/skills/hello","tree_hash":"sha256:abc","managed":false,"installed_at_utc":"2026-11-03T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "skills", "hello.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Read(dir, "skills", "hello")
	if err != nil || !ok {
		t.Fatalf("Read = %v, %v", ok, err)
	}
	if got.PlacedHash != "sha256:abc" || got.Rendered == nil || len(got.Rendered) != 0 {
		t.Errorf("legacy receipt read as placed_hash %q, rendered %#v", got.PlacedHash, got.Rendered)
	}
}

// TestRenderedRoundTrip: a rendered record's inputs come back as JSON that
// still decodes to the same value, whatever indentation Write gave them.
func TestRenderedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Receipt{
		Item: "hello", Version: "1.0.0", Placement: "skills", Path: "/roots/skills/hello",
		TreeHash: "sha256:abc", PlacedHash: "sha256:def",
		Rendered: []Rendered{{File: "a/b.json", Template: "a/b.json.tmpl", Inputs: json.RawMessage(`{"n":12345678901234567890,"list":["x","y"]}`)}},
	}
	if err := Write(dir, want); err != nil {
		t.Fatal(err)
	}
	got, _, err := Read(dir, "skills", "hello")
	if err != nil || len(got.Rendered) != 1 || got.Rendered[0].File != "a/b.json" || got.Rendered[0].Template != "a/b.json.tmpl" {
		t.Fatalf("Read = %+v, %v", got, err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, got.Rendered[0].Inputs); err != nil {
		t.Fatal(err)
	}
	if compact.String() != `{"n":12345678901234567890,"list":["x","y"]}` {
		t.Errorf("inputs = %s", compact.String())
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
