//go:build zip_only

package installer

import (
	"strings"
	"testing"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/status"
)

// A zip-only binary has no command installer or script runner to fall back
// on, so a command item that slips past process's validation must fail
// honestly in the report instead of reading as installed.
func TestZipOnlyCommandItemFails(t *testing.T) {
	statusCheckStatus = func(*status.Checker, catalog.Item, string, string) (bool, error) { return true, nil }
	t.Cleanup(func() { statusCheckStatus = (*status.Checker).CheckStatus })

	for _, tc := range []struct {
		action string
		item   catalog.Item
		want   string
	}{
		{"install", catalog.Item{Name: "Exe", Installer: catalog.InstallerItem{Type: "exe"}}, `installer type "exe" is not available`},
		{"uninstall", catalog.Item{Name: "Msi", Uninstaller: catalog.InstallerItem{Type: "msi"}}, `installer type "msi" is not available`},
		{"install", catalog.Item{Name: "Scripted", Installer: catalog.InstallerItem{Type: "exe"}, PreScript: "exit 0"}, "catalog scripts are not available"},
	} {
		r := &Runner{Report: report.New(), Checker: &status.Checker{}}
		if _, err := r.Install(tc.item, tc.action); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s: err = %v, want %q", tc.action, tc.item.Name, err, tc.want)
		}
		if len(r.Report.FailedItems) != 1 || len(r.Report.InstalledItems)+len(r.Report.UninstalledItems) != 0 {
			t.Errorf("%s %s: report = %+v, want one failure", tc.action, tc.item.Name, r.Report)
		}
	}
}
