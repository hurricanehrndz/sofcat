//go:build darwin

package policy

import (
	"log/slog"

	"github.com/hurricanehrndz/sofcat/pkg/config"
)

// readPolicyRoots returns the Machine and User entries of the WriteRoots
// policy in the managed preferences plist. A missing file has none; a file
// config.ReadManagedPlist does not trust, or that does not parse, is ignored
// with a warning.
func readPolicyRoots() (machine, user []string) {
	data, err := config.ReadManagedPlist()
	if err == nil && data != nil {
		machine, user, err = parsePlist(data)
	}
	if err != nil {
		slog.Warn("write roots policy ignored", "err", err)
		return nil, nil
	}
	return machine, user
}
