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
		slug, uiExe, wantName, wantData string
	}{
		{"sofcat", `C:\Program Files\SofCat\sofcat-ui.exe`, "sofcat-user-sync", `"C:\Program Files\SofCat\sofcat-ui.exe" --user-sync`},
		{"tapas-test", `C:\Program Files\Tapas Test\tapas-test-ui.exe`, "tapas-test-user-sync", `"C:\Program Files\Tapas Test\tapas-test-ui.exe" --user-sync`},
	}
	for _, tt := range tests {
		identity.Slug = tt.slug
		name, data := userSyncRunValue(tt.uiExe)
		if name != tt.wantName || data != tt.wantData {
			t.Errorf("userSyncRunValue(%q) = %q, %q; want %q, %q", tt.uiExe, name, data, tt.wantName, tt.wantData)
		}
	}
}
