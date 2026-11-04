//go:build zip_only

package installer

import (
	"errors"
	"fmt"

	"github.com/hurricanehrndz/sofcat/pkg/catalog"
)

// A zip-only build compiles no command installer and no catalog script runner
// (installer_command.go is left out), so its binary cannot run either.
// process rejects such items before they reach Install (process.ZipOnlyReason);
// these stand-ins fail any item that still does.
var (
	installItemFunc   = commandUnavailable("install")
	uninstallItemFunc = commandUnavailable("uninstall")
	itemScriptFunc    = func(_, kind, _ string) error {
		return fmt.Errorf("%s: %w", kind, errScriptsUnavailable)
	}
)

var errScriptsUnavailable = errors.New("catalog scripts are not available in this build")

// commandUnavailable returns an installItemFunc that records item as failed
// because its installer type is not compiled into this build.
func commandUnavailable(action string) func(*Runner, catalog.Item, string) (string, error) {
	return func(r *Runner, item catalog.Item, _ string) (string, error) {
		installerType := item.Installer.Type
		if action == "uninstall" {
			installerType = item.Uninstaller.Type
		}
		err := fmt.Errorf("installer type %q is not available in this build", installerType)
		r.emit(item, "failed", 0, err.Error())
		return "", r.recordFailure(item, action, err)
	}
}
