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
