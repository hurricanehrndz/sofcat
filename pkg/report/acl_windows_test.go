//go:build windows

package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS, which SDDL "FA" maps to.
const fileAllAccess = 0x1F01FF

// TestWriteInventoryProtectedDACL: the inventory must be readable only by
// SYSTEM and Administrators, and a file a user planted at the target must not
// keep its ACL after the run.
func TestWriteInventoryProtectedDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.json")
	// The planted file inherits the temp directory's ACL, which grants the
	// current user access: exactly what must not survive.
	if err := os.WriteFile(path, []byte("planted"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := WriteInventory(path, New().Inventory(Plan{})); err != nil {
		t.Fatalf("WriteInventory: %v", err)
	}

	assertInventoryDACL(t, path)
}

// TestCreateProtectedTempHasDACLAtCreation: the temp file must carry the
// inventory DACL from the moment it exists, or a standard user can open it
// through the directory's inherited ACL before the run writes the inventory.
func TestCreateProtectedTempHasDACLAtCreation(t *testing.T) {
	f, err := createProtectedTemp(t.TempDir(), ".inventory.json.")
	if err != nil {
		t.Fatalf("createProtectedTemp: %v", err)
	}
	defer func() { _ = f.Close() }()
	assertInventoryDACL(t, f.Name())
}

// assertInventoryDACL checks path has a protected DACL holding exactly SYSTEM
// full control and Administrators read.
func assertInventoryDACL(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	t.Logf("SDDL: %s", sd.String())
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("DACL is not protected (inheritance enabled)")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}

	want := map[windows.WELL_KNOWN_SID_TYPE]windows.ACCESS_MASK{
		windows.WinLocalSystemSid:           fileAllAccess,
		windows.WinBuiltinAdministratorsSid: windows.FILE_GENERIC_READ,
	}
	if dacl.AceCount != uint16(len(want)) {
		t.Errorf("DACL has %d ACEs, want %d", dacl.AceCount, len(want))
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatalf("GetAce %d: %v", i, err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		account, domain, _, _ := sid.LookupAccount("")
		t.Logf("ACE %d: type=%d sid=%s (%s\\%s) mask=0x%X", i, ace.Header.AceType, sid, domain, account, ace.Mask)
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("ACE %d is not an allow ACE", i)
			continue
		}
		matched := false
		for sidType, mask := range want {
			if sid.IsWellKnown(sidType) {
				matched = true
				if ace.Mask != mask {
					t.Errorf("ACE %d (%s): mask 0x%X, want 0x%X", i, sid, ace.Mask, mask)
				}
				delete(want, sidType)
			}
		}
		if !matched {
			t.Errorf("unexpected ACE %d for %s", i, sid)
		}
	}
	for sidType := range want {
		t.Errorf("missing ACE for well-known SID type %d", sidType)
	}
}

// TestWriteInventoryWaitsForReader: a reader holding inventory.json open
// without delete sharing, as Get-Content does while a poller reads it, makes
// the replacing rename fail with ERROR_ACCESS_DENIED. The writer must wait
// for the handle to close rather than leave the previous inventory in place.
func TestWriteInventoryWaitsForReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.json")
	if err := os.WriteFile(path, []byte("old"), 0o666); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open reader handle: %v", err)
	}
	const hold = 400 * time.Millisecond
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		_ = windows.CloseHandle(h)
		close(released)
	}()

	start := time.Now()
	err = WriteInventory(path, New().Inventory(Plan{ManifestName: "site_default"}))
	<-released
	if err != nil {
		t.Fatalf("WriteInventory while a reader held the target: %v", err)
	}
	if time.Since(start) < hold {
		t.Errorf("the rename succeeded in %v while the reader still held the file, so the retry was not exercised", time.Since(start))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Inventory
	if err = json.Unmarshal(data, &got); err != nil || got.ManifestName != "site_default" {
		t.Errorf("target not replaced: err=%v\n%s", err, data)
	}
}
