package installer

import (
	"fmt"
	"log/slog"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
	"github.com/hurricanehrndz/sofcat/pkg/report"
	"github.com/hurricanehrndz/sofcat/pkg/status"
)

// Fixtures shared by the tests that run in both the stock and the zip-only
// build.
var (
	// store original data to restore after each test
	origCheckStatus       = statusCheckStatus
	origInstallItemFunc   = installItemFunc
	origUninstallItemFunc = uninstallItemFunc

	// installItemURL stores the URL that `Install` generates during testing
	installItemURL string

	msiItem = catalog.Item{
		Installer: catalog.InstallerItem{
			Arguments: []string{`/L=1033`, `/S`},
			Hash:      `a1d4982abbb2bd2ccc238372ae688c790659c2c120efcee329fcca49c7c8fa9a`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64.msi`,
			Type:      `msi`,
		},
		Uninstaller: catalog.InstallerItem{
			Arguments: []string{`/U=1033`, `/S`},
			Hash:      `069068fea26346a7c006f39f8d84ced2ebb6b874a35143f52ed979d29f11ef3d`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64uninst.msi`,
			Type:      `msi`,
		},
		Version: "1.2.3",
	}

	// Define different options to bypass status checks during tests
	statusActionNoError   = `_sofcat_dev_action_noerror_`
	statusNoActionNoError = `_sofcat_dev_noaction_noerror_`
	statusActionError     = `_sofcat_dev_action_error_`
	statusNoActionError   = `_sofcat_dev_noaction_error_`
)

func fakeCheckStatus(_ *status.Checker, catalogItem catalog.Item, installType string, cachePath string) (install bool, checkErr error) {
	// Catch special names used in tests
	if catalogItem.DisplayName == statusActionNoError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return true, nil
	} else if catalogItem.DisplayName == statusNoActionNoError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return false, nil
	} else if catalogItem.DisplayName == statusActionError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return true, fmt.Errorf("testing %v", catalogItem.DisplayName)
	} else if catalogItem.DisplayName == statusNoActionError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return false, fmt.Errorf("testing %v", catalogItem.DisplayName)
	}

	fmt.Println(catalogItem.DisplayName)
	fmt.Println(installType)
	return false, nil
}

// newTestRunner returns a fresh run-scoped Runner wired for tests (K7: state
// is per-run, so tests no longer share or reset package globals).
func newTestRunner() *Runner {
	return &Runner{
		Report:      report.New(),
		Checker:     &status.Checker{},
		URLPackages: "https://example.com/",
		CachePath:   "testdata/",
	}
}

func fakeInstallItem(_ *Runner, item catalog.Item, itemURL string) (string, error) {
	installItemURL = itemURL
	return "", nil
}
