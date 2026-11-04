//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

const (
	admins    = "O:BA"
	inherited = "(A;ID;FA;;;SY)(A;ID;FA;;;BA)"
	dirInh    = "(A;OICIID;FA;;;SY)(A;OICIID;FA;;;BA)"
	usersRX   = "0x1200a9"
)

// TestProtectAppData: after protectAppData on <Name>\Data, a standard user
// must have no access to anything in the data directory, whatever ACLs and
// owners entries had before (as on a tree an older version created, where
// Users could create files), except the staged archives in user-artifacts,
// which the per-user helper reads. inventory.json keeps its stricter DACL, a
// junction a user planted is not followed, and the parent <Name> gets its
// own DACL without a walk: only administrators create entries there, and
// Users read sofcat's own placements beside Data.
func TestProtectAppData(t *testing.T) {
	parent := filepath.Join(t.TempDir(), identity.Name)
	root := filepath.Join(parent, "Data")
	artifacts := filepath.Join(root, "user-artifacts")
	marketplace := filepath.Join(parent, "marketplace")
	archive := filepath.Join(artifacts, "abc.zip")
	mkdir(t, root, filepath.Join(root, "cache", "packages"), artifacts, marketplace)
	write(t, filepath.Join(parent, "config.yaml"), archive,
		filepath.Join(root, "cache", "packages", "setup.msi"), filepath.Join(root, "service-manifest.yaml"),
		filepath.Join(root, inventoryFile))
	// A planted manifest that only Everyone may read, an archive locked to
	// SYSTEM, and inventory.json as pkg/report writes it.
	setDACL(t, filepath.Join(root, "service-manifest.yaml"), "D:P(A;;FA;;;WD)")
	setDACL(t, archive, "D:P(A;;FA;;;SY)")
	setDACL(t, filepath.Join(root, inventoryFile), "D:P(A;;FA;;;SY)(A;;FR;;;BA)")
	inventoryBefore := daclSDDL(t, filepath.Join(root, inventoryFile))

	canary := filepath.Join(t.TempDir(), "canary")
	mkdir(t, canary)
	setDACL(t, canary, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;WD)")
	canaryBefore := sddl(t, canary)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "cache", "j"), canary).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v: %s", err, out)
	}

	// Twice: the service applies it at every start.
	for range 2 {
		err := protectAppData(root)
		if err == nil || !strings.Contains(err.Error(), "not a plain file or directory") {
			t.Fatalf("protectAppData: %v, want only the junction reported", err)
		}
	}

	want := map[string]string{
		parent:                       admins + "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;" + usersRX + ";;;BU)",
		root:                         admins + "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		filepath.Join(root, "cache"): admins + "D:AI" + dirInh,
		filepath.Join(root, "cache", "packages", "setup.msi"): admins + "D:AI" + inherited,
		filepath.Join(root, "service-manifest.yaml"):          admins + "D:AI" + inherited,
		artifacts: admins + "D:AI(A;OICI;" + usersRX + ";;;BU)" + dirInh,
		archive:   admins + "D:AI(A;ID;" + usersRX + ";;;BU)" + inherited,
	}
	for path, w := range want {
		if got := sddl(t, path); got != w {
			t.Errorf("%s:\n got %s\nwant %s", path, got, w)
		}
	}
	// The parent is not walked: a placement there only inherits, keeping its
	// owner; config.yaml is protectConfig's.
	if got, w := daclSDDL(t, marketplace), "D:AI(A;OICIID;FA;;;SY)(A;OICIID;FA;;;BA)(A;OICIID;"+usersRX+";;;BU)"; got != w {
		t.Errorf("%s:\n got %s\nwant %s", marketplace, got, w)
	}
	if got := daclSDDL(t, filepath.Join(root, inventoryFile)); got != inventoryBefore {
		t.Errorf("inventory.json changed: got %s, want %s", got, inventoryBefore)
	}
	if got := sddl(t, canary); got != canaryBefore {
		t.Errorf("junction target changed:\n got %s\nwant %s", got, canaryBefore)
	}
}

// TestProtectAppDataLegacyRoot: an older config that still points
// app_data_path at the bare <Name> keeps its protection, including the
// staging directory, and nothing above it is touched.
func TestProtectAppDataLegacyRoot(t *testing.T) {
	base := t.TempDir()
	baseBefore := sddl(t, base)
	root := filepath.Join(base, strings.ToLower(identity.Name))
	mkdir(t, root)
	write(t, filepath.Join(root, "service-manifest.yaml"))
	if err := protectAppData(root); err != nil {
		t.Fatalf("protectAppData: %v", err)
	}
	want := map[string]string{
		root: admins + "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		filepath.Join(root, "service-manifest.yaml"): admins + "D:AI" + inherited,
		filepath.Join(root, "user-artifacts"):        admins + "D:AI(A;OICI;" + usersRX + ";;;BU)" + dirInh,
	}
	for path, w := range want {
		if got := sddl(t, path); got != w {
			t.Errorf("%s:\n got %s\nwant %s", path, got, w)
		}
	}
	if got := sddl(t, base); got != baseBefore {
		t.Errorf("parent of a legacy root changed:\n got %s\nwant %s", got, baseBefore)
	}
}

func TestProtectAppDataRefusesOtherNames(t *testing.T) {
	base := t.TempDir()
	for _, root := range []string{base, filepath.Join(base, "Other", "Data")} {
		mkdir(t, root)
		before := sddl(t, root)
		if err := protectAppData(root); err == nil || !strings.Contains(err.Error(), "not protecting") {
			t.Fatalf("%s: err = %v, want a refusal", root, err)
		}
		if got := sddl(t, root); got != before {
			t.Errorf("%s ACL changed:\n got %s\nwant %s", root, got, before)
		}
	}
}

// TestProtectConfig: config.yaml, which a user could create under
// ProgramData's default ACL before an administrator wrote one, ends up owned
// by Administrators with SYSTEM and Administrators only; a missing config is
// left alone and anything but a regular file is refused.
func TestProtectConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	write(t, cfg)
	setDACL(t, cfg, "D:P(A;;FA;;;WD)")
	for range 2 {
		if err := protectConfig(cfg); err != nil {
			t.Fatalf("protectConfig: %v", err)
		}
	}
	if got, want := sddl(t, cfg), admins+"D:PAI(A;;FA;;;SY)(A;;FA;;;BA)"; got != want {
		t.Errorf("config.yaml:\n got %s\nwant %s", got, want)
	}

	if err := protectConfig(filepath.Join(dir, "missing.yaml")); err != nil {
		t.Errorf("missing config: %v", err)
	}

	notFile := filepath.Join(dir, "dir.yaml")
	mkdir(t, notFile)
	before := sddl(t, notFile)
	if err := protectConfig(notFile); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory: err = %v, want a refusal", err)
	}
	if got := sddl(t, notFile); got != before {
		t.Errorf("directory ACL changed:\n got %s\nwant %s", got, before)
	}
}

func mkdir(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func setDACL(t *testing.T, path, s string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func sddl(t *testing.T, path string) string {
	t.Helper()
	return securityString(t, path, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
}

func daclSDDL(t *testing.T, path string) string {
	t.Helper()
	return securityString(t, path, windows.DACL_SECURITY_INFORMATION)
}

func securityString(t *testing.T, path string, info windows.SECURITY_INFORMATION) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo %s: %v", path, err)
	}
	return sd.String()
}
