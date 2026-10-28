package facts

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/conditions"
)

func TestCollectAdminFacts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"10-groups.ps1", "20-site.ps1", "30-bad.ps1", "40-fails.ps1", "50-spoof.ps1", ".hidden.ps1"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{
		"10-groups.ps1": `{"machine_groups": ["lab"], "site": "east"}`,
		"20-site.ps1":   `{"site": "west"}`,
		"30-bad.ps1":    `not json`,
		"50-spoof.ps1":  `{"hostname": "spoofed"}`,
	}
	var ran []string
	restore := stub(t, func(path string) ([]byte, error) {
		name := filepath.Base(path)
		ran = append(ran, name)
		if name == "40-fails.ps1" {
			return nil, errors.New("exit status 1")
		}
		return []byte(outputs[name]), nil
	}, func() conditions.Facts { return conditions.Facts{"hostname": "real"} })
	defer restore()

	facts := Collect(dir, true)
	if facts["site"] != "west" {
		t.Errorf("site = %v, want west (the later script wins)", facts["site"])
	}
	if groups, _ := facts["machine_groups"].([]any); len(groups) != 1 || groups[0] != "lab" {
		t.Errorf("machine_groups = %v", facts["machine_groups"])
	}
	if facts["hostname"] != "real" {
		t.Errorf("hostname = %v, want the built-in value", facts["hostname"])
	}
	if strings.Join(ran, ",") != "10-groups.ps1,20-site.ps1,30-bad.ps1,40-fails.ps1,50-spoof.ps1" {
		t.Errorf("ran %v; want name order, no dot-files or directories", ran)
	}

	// Without refresh the scripts do not run again.
	ran = nil
	if Collect(dir, false)["site"] != "west" || ran != nil {
		t.Errorf("Collect without refresh ran %v", ran)
	}
}

func TestCollectMissingDir(t *testing.T) {
	restore := stub(t, func(string) ([]byte, error) { t.Fatal("ran a script"); return nil, nil }, func() conditions.Facts { return conditions.Facts{"arch": "amd64"} })
	defer restore()
	facts := Collect(filepath.Join(t.TempDir(), "absent"), true)
	if len(facts) != 1 || facts["arch"] != "amd64" {
		t.Errorf("facts = %v", facts)
	}
}

func TestParseSMBIOS(t *testing.T) {
	var table []byte
	// Type 0 (BIOS), no strings.
	table = append(table, 0, 4, 0, 0, 0, 0)
	// Type 1: manufacturer=1, product=2, version=0, serial=3.
	table = append(table, 1, 8, 1, 0, 1, 2, 0, 3)
	table = append(table, "Lenovo\x0020XW0055US\x00PF1ABCDE \x00\x00"...)
	// Type 3: chassis type 10 (notebook) with the lock bit set.
	table = append(table, 3, 6, 2, 0, 1, 0x80|10)
	table = append(table, "Lenovo\x00\x00"...)
	table = append(table, 127, 4, 3, 0, 0, 0)

	raw := make([]byte, 8)
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(table)))
	raw = append(raw, table...)

	info := parseSMBIOS(raw)
	if info.manufacturer != "Lenovo" || info.model != "20XW0055US" || info.serial != "PF1ABCDE" {
		t.Errorf("info = %+v", info)
	}
	if info.machineType() != "laptop" {
		t.Errorf("machineType = %q, want laptop", info.machineType())
	}
	if (smbiosInfo{chassis: 3}).machineType() != "desktop" {
		t.Error("chassis 3 should be a desktop")
	}
	// Truncated input must not panic.
	for i := range raw {
		parseSMBIOS(raw[:i])
	}
}

func stub(t *testing.T, run func(string) ([]byte, error), builtins func() conditions.Facts) func() {
	t.Helper()
	oldRun, oldBuiltins := runScript, builtinFacts
	runScript, builtinFacts = run, builtins
	mu.Lock()
	adminCache, haveAdmin = nil, false
	mu.Unlock()
	return func() { runScript, builtinFacts = oldRun, oldBuiltins }
}
