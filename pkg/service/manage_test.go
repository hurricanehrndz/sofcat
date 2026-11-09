package service

import (
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/identity"
)

// The Run value is what starts the helper at logon, and a brand must get its
// own value, or two brands on one machine would overwrite each other's.
func TestUserSyncRunValue(t *testing.T) {
	origSlug := identity.Slug
	defer func() { identity.Slug = origSlug }()

	tests := []struct {
		slug, agentExe, wantName, wantData string
	}{
		{"sofcat", `C:\Program Files\SofCat\sofcat-useragent.exe`, "sofcat-user-sync", `"C:\Program Files\SofCat\sofcat-useragent.exe"`},
		{"tapas-test", `C:\Program Files\Tapas Test\tapas-test-useragent.exe`, "tapas-test-user-sync", `"C:\Program Files\Tapas Test\tapas-test-useragent.exe"`},
	}
	for _, tt := range tests {
		identity.Slug = tt.slug
		name, data := userSyncRunValue(tt.agentExe)
		if name != tt.wantName || data != tt.wantData {
			t.Errorf("userSyncRunValue(%q) = %q, %q; want %q, %q", tt.agentExe, name, data, tt.wantName, tt.wantData)
		}
	}
}

// -serviceremove ends only the agent beside the executable it removes: the
// same file name in another directory, such as a second brand's install or a
// copy under Downloads, is left running. Case does not matter on Windows.
func TestIsAgentImage(t *testing.T) {
	agents := []string{`C:\Program Files\SofCat\sofcat-useragent.exe`}
	tests := []struct {
		image string
		want  bool
	}{
		{`C:\Program Files\SofCat\sofcat-useragent.exe`, true},
		{`c:\program files\sofcat\SOFCAT-USERAGENT.EXE`, true},
		{`C:\Users\std\Downloads\sofcat-useragent.exe`, false},
		{`C:\Program Files\SofCat\sofcat.exe`, false},
		{`C:\Program Files\SofCat\sofcat-useragent.exe.bak`, false},
		{``, false},
	}
	for _, tt := range tests {
		if got := isAgentImage(tt.image, agents); got != tt.want {
			t.Errorf("isAgentImage(%q) = %v, want %v", tt.image, got, tt.want)
		}
	}
}
